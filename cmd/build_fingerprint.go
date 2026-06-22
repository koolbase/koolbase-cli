package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type fingerprintFile struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

type buildConfigFingerprint struct {
	Flavor          string            `json:"flavor,omitempty"`
	DartDefineKeys  []string          `json:"dart_define_keys,omitempty"`
	DartDefineHash  string            `json:"dart_define_hash,omitempty"`
	DartDefineFiles []fingerprintFile `json:"dart_define_files,omitempty"`
	Passthrough     []string          `json:"passthrough,omitempty"`
}

// buildFingerprint produces the release build-config fingerprint: the
// byte-affecting build flags, with dart-define VALUES never stored (they are
// frequently secrets). It captures flavor, the dart-define KEYS, a hash over the
// full normalized key=value set (drift detection without exposure), per-file
// content hashes for --dart-define-from-file, and the remaining passthrough with
// any define flags stripped out — so a secret routed via `-- --dart-define ...`
// is folded into the keys/hash and never echoed.
//
// Returns nil when there is nothing to record (a flag-less release), so the
// server stores SQL NULL and `patch push` degrades to the large-diff warning.
func buildFingerprint(flavor string, dartDefines, dartDefineFromFiles, passthrough []string) json.RawMessage {
	pDefines, pFiles, restPassthrough := splitDefinesFromPassthrough(passthrough)

	allDefines := append(append([]string{}, dartDefines...), pDefines...)
	allFiles := append(append([]string{}, dartDefineFromFiles...), pFiles...)

	fp := buildConfigFingerprint{
		Flavor:      flavor,
		Passthrough: restPassthrough,
	}

	if len(allDefines) > 0 {
		// Normalize: sort the KEY=VALUE set so order doesn't shift the hash.
		norm := append([]string{}, allDefines...)
		sort.Strings(norm)
		h := sha256.Sum256([]byte(strings.Join(norm, "\n")))
		fp.DartDefineHash = hex.EncodeToString(h[:])

		// Keys only, sorted + unique.
		seen := map[string]bool{}
		for _, d := range allDefines {
			k := d
			if i := strings.IndexByte(d, '='); i >= 0 {
				k = d[:i]
			}
			if !seen[k] {
				seen[k] = true
				fp.DartDefineKeys = append(fp.DartDefineKeys, k)
			}
		}
		sort.Strings(fp.DartDefineKeys)
	}

	for _, f := range allFiles {
		ff := fingerprintFile{Name: filepath.Base(f)}
		if data, err := os.ReadFile(f); err == nil {
			h := sha256.Sum256(data)
			ff.Hash = hex.EncodeToString(h[:])
		}
		fp.DartDefineFiles = append(fp.DartDefineFiles, ff)
	}

	if fp.Flavor == "" && len(fp.DartDefineKeys) == 0 &&
		len(fp.DartDefineFiles) == 0 && len(fp.Passthrough) == 0 {
		return nil
	}
	b, err := json.Marshal(fp)
	if err != nil {
		return nil
	}
	return json.RawMessage(b)
}

// splitDefinesFromPassthrough walks a `--` passthrough slice and pulls out any
// --dart-define / --dart-define-from-file flags (both joined and space-separated
// forms), returning the define values, the define-file paths, and the remaining
// passthrough with those flags removed.
func splitDefinesFromPassthrough(args []string) (defines, files, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dart-define":
			if i+1 < len(args) {
				defines = append(defines, args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--dart-define="):
			defines = append(defines, strings.TrimPrefix(a, "--dart-define="))
		case a == "--dart-define-from-file":
			if i+1 < len(args) {
				files = append(files, args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--dart-define-from-file="):
			files = append(files, strings.TrimPrefix(a, "--dart-define-from-file="))
		default:
			rest = append(rest, a)
		}
	}
	return defines, files, rest
}
