package cmd

// koolbase migrate firebase: a Firestore JSON export becomes a Koolbase
// import archive.
//
// The archive format is the contract between every converter and the API:
// one tar.gz of JSON and JSON Lines, described at docs.koolbase.com/migration.
// Converters live here, at the edge; the API only ever reads that one format.
//
// Input is the JSON shape the Firebase CLI and the common export scripts
// produce: a document per Firestore document, keyed by collection, then by
// id. Firestore's native LevelDB backup is not JSON and is not accepted.
//
//	{
//	  "users":  { "abc123": { "name": "Ama", "created": {"_seconds": 1690000000, "_nanoseconds": 0} } },
//	  "orders": { "xyz789": { "user": "abc123", "total": 50 } }
//	}
//
// What changes on the way through, and why:
//
//   - Firestore ids are 20-character strings; Koolbase ids are UUIDs. Every
//     document gets a UUID. A field that holds another document's Firestore
//     id is a reference, and it has to be rewritten to the new UUID — the
//     converter cannot guess which fields those are, so --ref names them:
//     --ref orders.user=users. That field is then declared a real reference
//     in the archive, enforced by the database from the first day.
//   - Subcollections (users/abc123/orders/xyz789) are flattened: orders becomes
//     a collection of its own with a users_id field and a reference to users.
//     Cleaner than nesting, and it is what the reference model is for.
//   - Firestore timestamps ({_seconds, _nanoseconds}) become ISO strings.
//     GeoPoints ({_latitude, _longitude}) become {lat, lng}.
//   - Firestore security rules do not translate. Every collection is
//     authenticated for read, write and delete, and the command says so.
//     Set the real rules in the dashboard after import.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Convert data from another platform into a Koolbase import archive",
}

var migrateFirebaseCmd = &cobra.Command{
	Use:   "firebase <export.json>",
	Short: "Convert a Firestore JSON export into a Koolbase import archive",
	Long: `Reads a Firestore export in JSON form and writes a Koolbase import archive,
ready for Backups → Import in the dashboard or POST /v1/projects/{id}/import.

Firestore ids become UUIDs. Name the fields that point at other documents so
they are rewritten and declared as references:

  koolbase migrate firebase export.json --ref orders.user=users --ref orders.product=products

Subcollections are flattened into collections of their own, each with a
<parent>_id field referencing the parent.

Security rules do not translate: every collection lands as authenticated.
Set the real rules in the dashboard after import.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, _ := cmd.Flags().GetString("out")
		refs, _ := cmd.Flags().GetStringArray("ref")
		return runMigrateFirebase(args[0], out, refs)
	},
}

func init() {
	migrateFirebaseCmd.Flags().String("out", "", "where to write the archive (default: <export>-koolbase.tar.gz)")
	migrateFirebaseCmd.Flags().StringArray("ref", nil, "a field that holds another document's id, as collection.field=target (repeatable)")
	migrateCmd.AddCommand(migrateFirebaseCmd)
	rootCmd.AddCommand(migrateCmd)
}

// ─── The archive's own view of the format ────────────────────────────────────
// Written independently of the API's Go types on purpose: the FORMAT is the
// contract. A converter that imported the server's types would tie every
// converter's build to the server.

type fbManifest struct {
	Version         int             `json:"version"`
	Kind            string          `json:"kind"`
	SourceProjectID string          `json:"source_project_id"`
	ExportedAt      time.Time       `json:"exported_at"`
	Collections     []fbManifestCol `json:"collections"`
}

type fbManifestCol struct {
	Name    string `json:"name"`
	Records int64  `json:"records"`
	File    string `json:"file"`
}

type fbSnapshotCollection struct {
	Name           string          `json:"name"`
	ReadRule       string          `json:"read_rule"`
	WriteRule      string          `json:"write_rule"`
	DeleteRule     string          `json:"delete_rule"`
	OwnerField     *string         `json:"owner_field"`
	RuleMode       string          `json:"rule_mode"`
	RuleConditions json.RawMessage `json:"rule_conditions"`
	AppendOnly     bool            `json:"append_only"`
}

type fbSnapshot struct {
	Version         int                    `json:"version"`
	Kind            string                 `json:"kind"`
	SourceProjectID string                 `json:"source_project_id"`
	CreatedAt       time.Time              `json:"created_at"`
	Collections     []fbSnapshotCollection `json:"collections"`
	Environments    []any                  `json:"environments"`
}

type fbReference struct {
	Collection       string `json:"collection"`
	Field            string `json:"field"`
	TargetCollection string `json:"target_collection"`
	OnDelete         string `json:"on_delete"`
}

type fbRecord struct {
	ID        string          `json:"id"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Revision  int64           `json:"revision"`
}

// ─── Conversion ──────────────────────────────────────────────────────────────

type refSpec struct{ collection, field, target string }

func parseRefs(specs []string) ([]refSpec, error) {
	var out []refSpec
	for _, s := range specs {
		lhs, target, ok := strings.Cut(s, "=")
		col, field, ok2 := strings.Cut(lhs, ".")
		if !ok || !ok2 || col == "" || field == "" || target == "" {
			return nil, fmt.Errorf("--ref %q: want collection.field=target", s)
		}
		out = append(out, refSpec{col, field, target})
	}
	return out, nil
}

// flatten walks the export and yields (collection, firestoreID, parentCollection, parentID, fields).
// A subcollection surfaces as its own collection; its documents remember the parent.
type fbDoc struct {
	collection, id      string
	parentCol, parentID string
	fields              map[string]any
}

func flattenFirestore(root map[string]any) []fbDoc {
	var docs []fbDoc
	var walk func(col string, docsIn map[string]any, parentCol, parentID string)
	walk = func(col string, docsIn map[string]any, parentCol, parentID string) {
		ids := make([]string, 0, len(docsIn))
		for id := range docsIn {
			ids = append(ids, id)
		}
		sort.Strings(ids) // stable output across runs
		for _, id := range ids {
			raw, ok := docsIn[id].(map[string]any)
			if !ok {
				continue
			}
			fields := map[string]any{}
			for k, v := range raw {
				// The two conventions export scripts use for subcollections.
				if k == "__collections__" {
					if subs, ok := v.(map[string]any); ok {
						for subName, subDocs := range subs {
							if sd, ok := subDocs.(map[string]any); ok {
								walk(subName, sd, col, id)
							}
						}
					}
					continue
				}
				fields[k] = v
			}
			docs = append(docs, fbDoc{col, id, parentCol, parentID, fields})
		}
	}
	names := make([]string, 0, len(root))
	for n := range root {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if d, ok := root[n].(map[string]any); ok {
			walk(n, d, "", "")
		}
	}
	return docs
}

// convertValue turns Firestore's special objects into plain JSON.
func convertValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if s, ok := t["_seconds"].(float64); ok {
			ns, _ := t["_nanoseconds"].(float64)
			return time.Unix(int64(s), int64(ns)).UTC().Format(time.RFC3339Nano)
		}
		if lat, ok := t["_latitude"].(float64); ok {
			lng, _ := t["_longitude"].(float64)
			return map[string]any{"lat": lat, "lng": lng}
		}
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = convertValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = convertValue(val)
		}
		return out
	default:
		return v
	}
}

func runMigrateFirebase(inPath, outPath string, refSpecs []string) error {
	refs, err := parseRefs(refSpecs)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("%s is not a JSON object keyed by collection: %w", inPath, err)
	}
	docs := flattenFirestore(root)
	if len(docs) == 0 {
		return fmt.Errorf("%s contains no documents", inPath)
	}

	// Pass 1: every document gets a UUID, remembered by (collection, firestore id).
	newID := map[string]string{}
	key := func(col, id string) string { return col + "/" + id }
	for _, d := range docs {
		newID[key(d.collection, d.id)] = uuid.NewString()
	}

	// Which fields are references, by collection.
	refByCol := map[string][]refSpec{}
	for _, r := range refs {
		refByCol[r.collection] = append(refByCol[r.collection], r)
	}
	// A flattened subcollection references its parent through <parent>_id.
	var declared []fbReference
	seenRef := map[string]bool{}
	addRef := func(col, field, target string) {
		k := col + "." + field
		if !seenRef[k] {
			seenRef[k] = true
			declared = append(declared, fbReference{col, field, target, "restrict"})
		}
	}

	// Pass 2: build records, rewriting references.
	now := time.Now().UTC()
	byCol := map[string][]fbRecord{}
	unresolved := 0
	for _, d := range docs {
		fields := map[string]any{}
		for k, v := range d.fields {
			fields[k] = convertValue(v)
		}
		if d.parentCol != "" {
			pf := d.parentCol + "_id"
			fields[pf] = newID[key(d.parentCol, d.parentID)]
			addRef(d.collection, pf, d.parentCol)
		}
		for _, r := range refByCol[d.collection] {
			old, ok := fields[r.field].(string)
			if !ok || old == "" {
				continue
			}
			if id, found := newID[key(r.target, old)]; found {
				fields[r.field] = id
			} else {
				// A reference to a document the export does not contain. The
				// import would refuse the whole archive; clear it and say so.
				fields[r.field] = nil
				unresolved++
			}
			addRef(d.collection, r.field, r.target)
		}
		data, _ := json.Marshal(fields)
		created := now
		if ts, ok := fields["createdAt"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				created = t
			}
		}
		byCol[d.collection] = append(byCol[d.collection], fbRecord{
			ID: newID[key(d.collection, d.id)], Data: data,
			CreatedAt: created, UpdatedAt: created, Revision: 1,
		})
	}

	// Structure: every collection authenticated, since Firestore rules do not translate.
	cols := make([]string, 0, len(byCol))
	for c := range byCol {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	snap := fbSnapshot{Version: 2, Kind: "koolbase.snapshot", SourceProjectID: "firebase", CreatedAt: now, Environments: []any{}}
	for _, c := range cols {
		snap.Collections = append(snap.Collections, fbSnapshotCollection{
			Name: c, ReadRule: "authenticated", WriteRule: "authenticated", DeleteRule: "authenticated",
			RuleMode: "all", RuleConditions: json.RawMessage("[]"),
		})
	}

	// Write the archive. manifest.json last, as the format requires.
	if outPath == "" {
		outPath = strings.TrimSuffix(inPath, ".json") + "-koolbase.tar.gz"
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	add := func(name string, b []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), ModTime: now}); err != nil {
			return err
		}
		_, err := tw.Write(b)
		return err
	}
	addJSON := func(name string, v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		return add(name, b)
	}

	manifest := fbManifest{Version: 1, Kind: "koolbase.export", SourceProjectID: "firebase", ExportedAt: now}
	for _, c := range cols {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		for _, r := range byCol[c] {
			enc.Encode(r)
		}
		file := "collections/" + c + ".jsonl"
		if err := add(file, buf.Bytes()); err != nil {
			return err
		}
		manifest.Collections = append(manifest.Collections, fbManifestCol{c, int64(len(byCol[c])), file})
	}
	if declared == nil {
		declared = []fbReference{}
	}
	for _, e := range []struct {
		name string
		v    any
	}{
		{"snapshot.json", snap},
		{"references.json", declared},
		{"constraints.json", []any{}},
		{"storage.json", []any{}},
		{"manifest.json", manifest},
	} {
		if err := addJSON(e.name, e.v); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}

	total := 0
	for _, c := range cols {
		total += len(byCol[c])
	}
	fmt.Printf("Wrote %s\n", outPath)
	fmt.Printf("  %d collections, %d records, %d references declared\n", len(cols), total, len(declared))
	if unresolved > 0 {
		fmt.Printf("  %d reference values pointed at documents the export does not contain and were cleared\n", unresolved)
	}
	fmt.Println("  Every collection is authenticated for read, write and delete — Firestore rules do not translate.")
	fmt.Println("  Set the real rules in the dashboard after import.")
	return nil
}
