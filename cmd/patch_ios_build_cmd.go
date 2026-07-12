package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/kennedyowusu/koolbase-cli/internal/engine"
	"github.com/spf13/cobra"
)

// patchIosBuildCmd authors an iOS code-push patch from a developer's changed
// Dart source: compiles it to bytecode, computes the identity keytable, and
// packs a KBPI container — the one-command sibling of Android's `patch push`.
//
// It automates the previously-manual pipeline:
//  1. Resolve the Koolbase engine (dart2bytecode, host dart, vm_platform.dill).
//  2. Write a remapped package_config so the patch compiles AS the host
//     package (identity of the changed fn reverses to the host function).
//  3. Run dart2bytecode --koolbase-kbpi (bytecode + SHA-256 identity keytable).
//
// Output is a KBPI ready for `koolbase patch ios --kbpi` (to sign) or, with
// --key, this command signs it into a KBPM in one shot.
var patchIosBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Author an iOS patch from changed Dart source (compile → keytable → KBPI)",
	RunE: func(cmd *cobra.Command, args []string) error {
		source, _ := cmd.Flags().GetString("source")
		appPackage, _ := cmd.Flags().GetString("app-package")
		version, _ := cmd.Flags().GetString("engine")
		outPath, _ := cmd.Flags().GetString("output")

		if source == "" {
			return fmt.Errorf("--source is required (the changed Dart file, e.g. patch_pkg/lib/main.dart)")
		}
		if appPackage == "" {
			return fmt.Errorf("--app-package is required (host package name, e.g. 'ripple')")
		}
		if version == "" {
			return fmt.Errorf("--engine is required (installed Koolbase engine, e.g. 3.44.4-koolbase.2)")
		}

		absSource, err := filepath.Abs(source)
		if err != nil {
			return fmt.Errorf("resolve --source: %w", err)
		}
		if _, err := os.Stat(absSource); err != nil {
			return fmt.Errorf("--source not found: %s", absSource)
		}
		// The package root is the parent of the source's lib/ dir. dart2bytecode
		// resolves `package:<app>/main.dart` against packageUri "lib/" under this
		// root, so the source must live at <root>/lib/<name>.dart.
		libDir := filepath.Dir(absSource)      // .../patch_pkg/lib
		pkgRoot := filepath.Dir(libDir)        // .../patch_pkg
		sourceBase := filepath.Base(absSource) // main.dart

		// Resolve engine paths.
		engineDir, err := engine.VersionDir(version)
		if err != nil {
			return fmt.Errorf("engine %q not installed (koolbase engine install %s --target-platform ios): %w", version, version, err)
		}
		srcOut := filepath.Join(engineDir, "src", "out")
		if _, err := os.Stat(srcOut); err != nil {
			// fall back to engineDir/out
			srcOut = filepath.Join(engineDir, "out")
		}
		dartBin := filepath.Join(srcOut, "host_release_arm64", "dart-sdk", "bin", "dartaotruntime_d2b")

		d2b := filepath.Join(srcOut, "host_release_arm64", "dart-sdk", "bin",
			"snapshots", "dart2bytecode.dart.snapshot")
		platformDill := filepath.Join(srcOut, "host_release_arm64", "dart-sdk", "lib", "_internal", "vm_platform.dill")

		for _, p := range []struct{ label, path string }{
			{"host dart", dartBin}, {"dart2bytecode", d2b}, {"vm_platform.dill", platformDill},
		} {
			if _, err := os.Stat(p.path); err != nil {
				return fmt.Errorf("%s not found in engine %s: %s", p.label, version, p.path)
			}
		}

		// Write the remapped package_config: <app-package> -> the patch pkg root,
		// so the patch compiles as package:<app-package>/<source>.
		pcPath := filepath.Join(os.TempDir(), "koolbase_patch_pkgconfig_"+appPackage+".json")
		pc := fmt.Sprintf(`{
  "configVersion": 2,
  "packages": [
    { "name": "%s", "rootUri": "file://%s", "packageUri": "lib/", "languageVersion": "3.12" }
  ]
}`, appPackage, pkgRoot)
		if err := os.WriteFile(pcPath, []byte(pc), 0o644); err != nil {
			return fmt.Errorf("write package_config: %w", err)
		}
		defer os.Remove(pcPath)

		if outPath == "" {
			outPath = filepath.Join(filepath.Dir(pkgRoot), appPackage+"_patch.kbpi")
		}

		inputUri := fmt.Sprintf("package:%s/%s", appPackage, sourceBase)
		fmt.Printf("Authoring iOS patch\n")
		fmt.Printf("  engine:   %s\n", version)
		fmt.Printf("  source:   %s\n", absSource)
		fmt.Printf("  compiles as: %s\n", inputUri)
		fmt.Printf("  output:   %s\n\n", outPath)

		d2bArgs := []string{
			d2b,
			"--platform", platformDill,
			"--packages", pcPath,
			"--prefix-library-uris", "koolbase_patch",
			"--koolbase-kbpi",
			"-o", outPath,
			inputUri,
		}
		c := exec.Command(dartBin, d2bArgs...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("dart2bytecode failed: %w", err)
		}

		fmt.Printf("\n✓ KBPI written: %s\n", outPath)
		fmt.Printf("  Next: koolbase patch ios --kbpi %s --binary <Runner.app> --key <ed25519.key>\n", outPath)
		fmt.Printf("        (to sign into a KBPM), then koolbase patch push-ios to deliver.\n")
		return nil
	},
}

func init() {
	patchIosBuildCmd.Flags().String("source", "", "Path to the changed Dart file (e.g. patch_pkg/lib/main.dart)")
	patchIosBuildCmd.Flags().String("app-package", "", "Host app package name (e.g. 'ripple') — drives patch identity")
	patchIosBuildCmd.Flags().String("engine", "", "Installed Koolbase engine version (e.g. 3.44.4-koolbase.2)")
	patchIosBuildCmd.Flags().String("output", "", "Output KBPI path (default: <app-package>_patch.kbpi)")
	patchIosCmd.AddCommand(patchIosBuildCmd)
}
