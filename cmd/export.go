package cmd

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// The export contract, enforced. The Designer produces a zip: a generated
// tree under lib/generated/, a handful of developer-owned files written
// once, and .koolbase/export.json recording a hash of every generated
// file. This command applies it to a project directory.
//
//   First apply    everything is written.
//   Re-apply       lib/generated/ is replaced transactionally; developer
//                  files are never touched; hand edits inside generated/
//                  are detected from the previous manifest and named
//                  before anything is overwritten; a pubspec whose
//                  dependencies changed is reported, not rewritten.
//
// No merge. Inside generated/ Koolbase owns it; outside, the developer
// does.

const manifestPath = ".koolbase/export.json"

type manifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type exportManifest struct {
	DocumentID       string         `json:"document_id"`
	DocumentName     string         `json:"document_name"`
	SchemaVersion    int            `json:"schema_version"`
	ExporterVersion  int            `json:"exporter_version"`
	GeneratedRoots   []string       `json:"generated_roots"`
	OwnedByDeveloper []string       `json:"owned_by_developer"`
	GeneratedFiles   []manifestFile `json:"generated_files"`
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Apply a Designer export to a Flutter project",
}

var exportApplyCmd = &cobra.Command{
	Use:   "apply <export.zip>",
	Short: "Write or update a project from a Designer export",
	Long: `Applies a Koolbase Designer export to a project directory.

On a fresh directory every file is written. On an existing project only
the generated folders are replaced (lib/generated/ for Flutter; src/generated/
and app/(koolbase)/ for Expo); main.dart, pubspec.yaml, package.json and anything you have
added are never touched. If generated files were edited by hand since the
last export, they are listed and the apply stops unless --force is given.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		into, _ := cmd.Flags().GetString("into")
		force, _ := cmd.Flags().GetBool("force")
		if into == "" {
			into = "."
		}
		return applyExport(args[0], into, force, cmd.OutOrStdout())
	},
}

func init() {
	exportApplyCmd.Flags().String("into", ".", "Project directory to apply into")
	exportApplyCmd.Flags().Bool("force", false, "Replace generated files even if they were edited by hand")
	exportCmd.AddCommand(exportApplyCmd)
	rootCmd.AddCommand(exportCmd)
}

func applyExport(zipPath, into string, force bool, out io.Writer) error {
	incoming, err := readZip(zipPath)
	if err != nil {
		return err
	}
	raw, ok := incoming[manifestPath]
	if !ok {
		return fmt.Errorf("%s is not a Koolbase export: no %s", zipPath, manifestPath)
	}
	var next exportManifest
	if err := json.Unmarshal(raw, &next); err != nil {
		return fmt.Errorf("export manifest is unreadable: %w", err)
	}
	if len(next.GeneratedRoots) == 0 {
		return fmt.Errorf("export manifest names no generated roots")
	}

	prevPath := filepath.Join(into, manifestPath)
	prevRaw, err := os.ReadFile(prevPath)
	fresh := os.IsNotExist(err)
	if err != nil && !fresh {
		return fmt.Errorf("reading previous manifest: %w", err)
	}

	if fresh {
		return applyFresh(incoming, next.GeneratedRoots, into, out)
	}

	var prev exportManifest
	if err := json.Unmarshal(prevRaw, &prev); err != nil {
		return fmt.Errorf("previous manifest is unreadable: %w", err)
	}
	if prev.DocumentID != next.DocumentID {
		return fmt.Errorf("this directory was exported from document %s; the zip is from %s — apply into a different directory",
			prev.DocumentID, next.DocumentID)
	}

	// Hand edits inside generated/ since the last apply. Detected against
	// the PREVIOUS manifest's hashes: a file whose bytes differ from what
	// Koolbase last wrote was touched by someone. Named, never merged.
	edited := detectEdits(prev, into)

	// A file of theirs inside a generated root. The swap would delete
	// it, so say so before that happens rather than after.
	if unexpected := detectUnexpected(prev, into); len(unexpected) > 0 && !force {
		fmt.Fprintf(out, "Files inside %s that Koolbase did not write:\n", rootsText(next.GeneratedRoots))
		for _, p := range unexpected {
			fmt.Fprintf(out, "  %s\n", p)
		}
		fmt.Fprintln(out, "\nRe-export replaces the whole generated tree and would remove them.")
		fmt.Fprintf(out, "Move them outside %s, or run again with --force.\n", rootsText(next.GeneratedRoots))
		return fmt.Errorf("stopped: %d unexpected file(s) in the generated tree", len(unexpected))
	}

	if len(edited) > 0 && !force {
		fmt.Fprintln(out, "Generated files were edited by hand since the last export:")
		for _, p := range edited {
			fmt.Fprintf(out, "  %s\n", p)
		}
		fmt.Fprintf(out, "\nRe-export replaces them. Move your changes outside %s,\n", rootsText(next.GeneratedRoots))
		fmt.Fprintln(out, "or run again with --force to discard them.")
		return fmt.Errorf("stopped: %d generated file(s) edited", len(edited))
	}

	return applyReplace(incoming, next, prev, into, edited, out)
}

func applyFresh(incoming map[string][]byte, roots []string, into string, out io.Writer) error {
	paths := sortedKeys(incoming)
	for _, p := range paths {
		if err := writeFile(filepath.Join(into, p), incoming[p]); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "Exported %d files into %s\n", len(paths), into)
	fmt.Fprintf(out, "\n%s: Koolbase's, replaced on re-export.\n", rootsText(roots))
	fmt.Fprintf(out, "Everything else is yours; build outside %s.\n", rootsText(roots))
	return nil
}

func applyReplace(incoming map[string][]byte, next, prev exportManifest, into string, edited []string, out io.Writer) error {
	// Transactional per root: build each new tree beside the old one and
	// swap only after every file is written. A failure part-way leaves the
	// existing tree untouched. Roots are independent — lib/generated/ and
	// test/generated/ today.
	written := 0
	for _, root := range next.GeneratedRoots {
		n, err := replaceRoot(incoming, root, into)
		if err != nil {
			return err
		}
		written += n
	}

	if err := writeFile(filepath.Join(into, manifestPath), incoming[manifestPath]); err != nil {
		return err
	}

	// Removed screens: files the previous manifest listed that the new one
	// does not. The swap above already dropped them; say so.
	nextSet := map[string]bool{}
	for _, f := range next.GeneratedFiles {
		nextSet[f.Path] = true
	}
	var removed []string
	for _, f := range prev.GeneratedFiles {
		if !nextSet[f.Path] {
			removed = append(removed, f.Path)
		}
	}

	fmt.Fprintf(out, "Replaced %s (%d files)\n", strings.Join(next.GeneratedRoots, ", "), written)
	if len(removed) > 0 {
		fmt.Fprintf(out, "Removed %d generated file(s) no longer in the design:\n", len(removed))
		for _, p := range removed {
			fmt.Fprintf(out, "  %s\n", p)
		}
	}
	if len(edited) > 0 {
		fmt.Fprintf(out, "Discarded hand edits in %d file(s) (--force)\n", len(edited))
	}

	// Developer-owned files: never written. But pubspec dependencies can
	// change between exports, and silently rewriting it would break the
	// rule the moment it was convenient. Report the delta instead.
	reportPubspecDelta(incoming, into, out)
	reportPackageJSONDelta(incoming, into, out)
	fmt.Fprintln(out, "\nDeveloper files untouched: "+strings.Join(next.OwnedByDeveloper, ", "))
	return nil
}

func replaceRoot(incoming map[string][]byte, root, into string) (int, error) {
	liveDir := filepath.Join(into, filepath.Clean(root))
	stageDir := liveDir + ".koolbase-next"
	backupDir := liveDir + ".koolbase-prev"
	_ = os.RemoveAll(stageDir)
	_ = os.RemoveAll(backupDir)

	written := 0
	for _, p := range sortedKeys(incoming) {
		if !strings.HasPrefix(p, root) {
			continue
		}
		rel := strings.TrimPrefix(p, root)
		if err := writeFile(filepath.Join(stageDir, rel), incoming[p]); err != nil {
			_ = os.RemoveAll(stageDir)
			return 0, err
		}
		written++
	}
	if written == 0 {
		_ = os.RemoveAll(stageDir)
		return 0, nil
	}

	if err := os.Rename(liveDir, backupDir); err != nil && !os.IsNotExist(err) {
		_ = os.RemoveAll(stageDir)
		return 0, fmt.Errorf("moving old %s aside: %w", root, err)
	}
	if err := os.Rename(stageDir, liveDir); err != nil {
		_ = os.Rename(backupDir, liveDir)
		return 0, fmt.Errorf("swapping in new %s: %w", root, err)
	}
	_ = os.RemoveAll(backupDir)
	return written, nil
}

func detectEdits(prev exportManifest, into string) []string {
	var edited []string
	for _, f := range prev.GeneratedFiles {
		b, err := os.ReadFile(filepath.Join(into, f.Path))
		if err != nil {
			continue // deleted by hand: not an edit to preserve
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			edited = append(edited, f.Path)
		}
	}
	sort.Strings(edited)
	return edited
}

// detectUnexpected finds files inside a generated root that the
// previous manifest never listed.
//
// The swap replaces a root wholesale, so anything in there that
// Koolbase did not write is deleted. The contract says the directory is
// Koolbase's, and the backup makes it recoverable -- but a developer
// who put a file there and lost it without being told would be right to
// be angry, and "you should have read the header comment" is not an
// answer.
func detectUnexpected(prev exportManifest, into string) []string {
	known := map[string]bool{}
	for _, f := range prev.GeneratedFiles {
		known[filepath.Clean(f.Path)] = true
	}

	var found []string
	for _, root := range prev.GeneratedRoots {
		dir := filepath.Join(into, filepath.Clean(root))
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(into, path)
			if relErr != nil {
				return nil
			}
			if !known[filepath.Clean(rel)] {
				found = append(found, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	sort.Strings(found)
	return found
}

func reportPubspecDelta(incoming map[string][]byte, into string, out io.Writer) {
	want, ok := incoming["pubspec.yaml"]
	if !ok {
		return
	}
	have, err := os.ReadFile(filepath.Join(into, "pubspec.yaml"))
	if err != nil {
		return
	}
	wantDeps := depsOf(string(want))
	haveDeps := depsOf(string(have))
	var missing []string
	for d := range wantDeps {
		if _, ok := haveDeps[d]; !ok {
			missing = append(missing, d+": "+wantDeps[d])
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	fmt.Fprintln(out, "\nGenerated code now needs dependencies your pubspec.yaml does not list:")
	for _, m := range missing {
		fmt.Fprintf(out, "  %s\n", m)
	}
	fmt.Fprintln(out, "pubspec.yaml is yours; add them and run `flutter pub get`.")
}

// depsOf reads the `dependencies:` block of a pubspec — name -> constraint.
// Deliberately not a YAML parser: the block the exporter writes is flat,
// and a developer's additions in the same block are flat too.
func depsOf(pubspec string) map[string]string {
	deps := map[string]string{}
	in := false
	for _, line := range strings.Split(pubspec, "\n") {
		if strings.HasPrefix(line, "dependencies:") {
			in = true
			continue
		}
		if in && len(line) > 0 && line[0] != ' ' {
			break
		}
		if !in {
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "sdk:") {
			continue
		}
		if k, v, ok := strings.Cut(t, ":"); ok {
			deps[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return deps
}

func readZip(path string) (map[string][]byte, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer r.Close()
	// Is the whole zip inside one wrapping folder? True when no entry
	// starts with .koolbase/ and every entry shares the same first segment.
	wrapped := ""
	first := ""
	allShare := true
	for _, f := range r.File {
		name := filepath.ToSlash(f.Name)
		if strings.HasPrefix(name, ".koolbase/") {
			allShare = false
			break
		}
		seg, _, ok := strings.Cut(name, "/")
		if !ok {
			allShare = false
			break
		}
		if first == "" {
			first = seg
		} else if seg != first {
			allShare = false
			break
		}
	}
	if allShare && first != "" {
		wrapped = first + "/"
	}
	files := map[string][]byte{}
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(f.Name)
		// The Designer's zip is rooted at the project: lib/, test/, .koolbase/
		// and the top-level files. A zip re-packed by hand with a wrapping
		// folder is handled by stripping exactly one leading folder when
		// nothing in the zip starts with .koolbase/. Decided once, below,
		// not guessed per entry — a per-entry guess stripped test/.
		if wrapped != "" && strings.HasPrefix(name, wrapped) {
			name = strings.TrimPrefix(name, wrapped)
		}
		if strings.Contains(name, "..") {
			return nil, fmt.Errorf("refusing zip entry with path traversal: %s", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		files[name] = b
	}
	return files, nil
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func sortedKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// reportPackageJSONDelta is the Expo twin of reportPubspecDelta. package.json
// is the developer's, so a dependency the generated code now needs is
// reported, never written. Only an export that carries a package.json (an
// Expo export) reaches past the first check; Flutter exports never do.
func reportPackageJSONDelta(incoming map[string][]byte, into string, out io.Writer) {
	want, ok := incoming["package.json"]
	if !ok {
		return
	}
	have, err := os.ReadFile(filepath.Join(into, "package.json"))
	if err != nil {
		return
	}
	wantDeps, err1 := npmDepsOf(want)
	haveDeps, err2 := npmDepsOf(have)
	if err1 != nil || err2 != nil {
		fmt.Fprintln(out, "\nCould not read package.json to compare dependencies; check them by hand.")
		return
	}
	var missing, names []string
	for d, v := range wantDeps {
		if _, ok := haveDeps[d]; !ok {
			missing = append(missing, d+": "+v)
			names = append(names, d)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	sort.Strings(names)
	fmt.Fprintln(out, "\nGenerated code now needs dependencies your package.json does not list:")
	for _, m := range missing {
		fmt.Fprintf(out, "  %s\n", m)
	}
	fmt.Fprintf(out, "package.json is yours; add them with `npx expo install %s`.\n", strings.Join(names, " "))
}

// npmDepsOf reads a package.json's dependencies and devDependencies,
// name -> version.
func npmDepsOf(pkg []byte) (map[string]string, error) {
	var p struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(pkg, &p); err != nil {
		return nil, err
	}
	deps := map[string]string{}
	for k, v := range p.Dependencies {
		deps[k] = v
	}
	for k, v := range p.DevDependencies {
		deps[k] = v
	}
	return deps, nil
}

// rootsText names the export's generated roots for a message, from the
// manifest -- never a hard-coded path, since Flutter and Expo exports own
// different folders.
func rootsText(roots []string) string {
	switch len(roots) {
	case 0:
		return "the generated folders"
	case 1:
		return roots[0]
	default:
		return strings.Join(roots[:len(roots)-1], ", ") + " and " + roots[len(roots)-1]
	}
}
