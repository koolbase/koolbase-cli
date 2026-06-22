package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
)

// printReleaseFlagParity prints the build-config fingerprint recorded on a
// matched release so the developer can confirm their --new binary was built with
// the same byte-affecting flags. Silent when the release carries no fingerprint
// (pre-Chunk-2 releases, or one auto-created by patch push) — the large-diff
// warning remains the fallback. dart-define VALUES are never shown; only keys.
func printReleaseFlagParity(rel *api.Release) {
	if rel == nil || len(rel.BuildConfig) == 0 {
		return
	}
	var fp struct {
		Flavor          string   `json:"flavor"`
		DartDefineKeys  []string `json:"dart_define_keys"`
		DartDefineFiles []struct {
			Name string `json:"name"`
		} `json:"dart_define_files"`
		Passthrough []string `json:"passthrough"`
	}
	if err := json.Unmarshal(rel.BuildConfig, &fp); err != nil {
		return
	}
	if fp.Flavor == "" && len(fp.DartDefineKeys) == 0 &&
		len(fp.DartDefineFiles) == 0 && len(fp.Passthrough) == 0 {
		return
	}

	fmt.Println("\n  This release was built with these flags — your --new binary must match:")
	if fp.Flavor != "" {
		fmt.Printf("    flavor:                %s\n", fp.Flavor)
	}
	if len(fp.DartDefineKeys) > 0 {
		fmt.Printf("    dart-define keys:      %s  (values hashed, not shown)\n", strings.Join(fp.DartDefineKeys, ", "))
	}
	if len(fp.DartDefineFiles) > 0 {
		names := make([]string, len(fp.DartDefineFiles))
		for i, f := range fp.DartDefineFiles {
			names[i] = f.Name
		}
		fmt.Printf("    dart-define-from-file: %s\n", strings.Join(names, ", "))
	}
	if len(fp.Passthrough) > 0 {
		fmt.Printf("    passthrough:           %s\n", strings.Join(fp.Passthrough, " "))
	}
	fmt.Println("    A mismatch here is the usual cause of an oversized diff that won't apply cleanly.")
}
