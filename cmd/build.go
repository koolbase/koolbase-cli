package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/kennedyowusu/koolbase-cli/internal/engine"
	"github.com/spf13/cobra"
)

var (
	buildRelease    bool
	buildVersion    string
	buildFlutterSDK string
	buildTreeShake  bool
)

var buildCmd = &cobra.Command{
	Use:   "build [platform]",
	Short: "Build your Flutter app with the Koolbase engine (enables Code Push)",
	Long: `Build a Flutter app using the installed Koolbase engine so the result
supports Code Push.

This wraps 'flutter build' with the correct --local-engine flags pointing at
the Koolbase engine you installed via 'koolbase engine install'. You don't need
to manage those flags yourself.

IMPORTANT: the Flutter SDK version must match the engine's Flutter version
(e.g. an engine built for Flutter 3.22.3 needs the 3.22.3 SDK). Point Koolbase
at the matching SDK with --flutter-sdk, or save it once:

  koolbase build macos --release --flutter-sdk ~/flutter-3.22.3

Examples:
  koolbase build macos --release --flutter-sdk ~/flutter-3.22.3
  koolbase build macos --release --engine 3.22.3-koolbase.1`,
	Args: cobra.ExactArgs(1),
	RunE: runBuild,
}

func runBuild(cmd *cobra.Command, args []string) error {
	platform := args[0]
	if platform != "macos" {
		return fmt.Errorf("platform %q not supported yet — only 'macos' is available today", platform)
	}

	// Resolve which engine to use.
	version := buildVersion
	if version == "" {
		resolved, err := resolveProjectEngine()
		if err != nil {
			return err
		}
		version = resolved
	}

	installed, err := engine.IsInstalled(version)
	if err != nil {
		return err
	}
	if !installed {
		return fmt.Errorf("engine %s not installed — run: koolbase engine install %s",
			version, strings.TrimSuffix(version, "-koolbase.1"))
	}

	engineDir, err := engine.VersionDir(version)
	if err != nil {
		return err
	}

	// The engine archive lays out as {version}/src/out/mac_release_arm64.
	// Flutter's --local-engine-src-path wants the dir containing out/.
	srcPath := filepath.Join(engineDir, "src")
	if _, err := os.Stat(filepath.Join(srcPath, "out")); err != nil {
		// Some archives may not nest under src/. Fall back to engineDir.
		if _, err2 := os.Stat(filepath.Join(engineDir, "out")); err2 == nil {
			srcPath = engineDir
		} else {
			return fmt.Errorf("engine layout unexpected: no out/ under %s", engineDir)
		}
	}

	localEngine := "mac_release_" + hostArch()

	// Resolve the flutter binary. The SDK version MUST match the engine's
	// Flutter version or Dart compilation fails (framework source compiled
	// against a mismatched Dart SDK). Resolution order:
	//   1. --flutter-sdk flag
	//   2. saved config (FlutterSDKPath)
	//   3. flutter on PATH (with a mismatch caveat printed)
	flutterBin, sdkSource, err := resolveFlutterBin(version)
	if err != nil {
		return err
	}

	flutterArgs := []string{
		"build", "macos",
		"--local-engine=" + localEngine,
		"--local-engine-host=" + localEngine,
		"--local-engine-src-path=" + srcPath,
	}
	if buildRelease {
		flutterArgs = append(flutterArgs, "--release")
	}
	// The currently-distributed engine may not bundle const_finder.dart.snapshot,
	// which the icon tree-shaker requires. Default to skipping tree-shaking so the
	// build succeeds; pass --tree-shake-icons once using an engine that ships the
	// host tools.
	if !buildTreeShake {
		flutterArgs = append(flutterArgs, "--no-tree-shake-icons")
	}

	fmt.Printf("Building macos with Koolbase engine %s\n", version)
	fmt.Printf("  Flutter SDK: %s (%s)\n", flutterBin, sdkSource)
	fmt.Printf("  flutter %s\n\n", strings.Join(flutterArgs, " "))

	build := exec.Command(flutterBin, flutterArgs...)
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	build.Stdin = os.Stdin
	if err := build.Run(); err != nil {
		return fmt.Errorf("flutter build failed: %w", err)
	}

	fmt.Println("\n✓ Build complete. This binary supports Koolbase Code Push.")
	return nil
}

// resolveFlutterBin returns the path to the flutter binary to use, plus a short
// human-readable description of where it came from. engineVersion is used only
// to render helpful guidance (e.g. "3.22.3" from "3.22.3-koolbase.1").
//
// Resolution order: --flutter-sdk flag, then saved config, then PATH. When
// falling back to PATH, a caveat is printed because PATH flutter is very likely
// the developer's day-to-day SDK, which usually won't match the engine version.
func resolveFlutterBin(engineVersion string) (bin string, source string, err error) {
	flutterVersion := strings.TrimSuffix(engineVersion, "-koolbase.1")

	// 1. Explicit flag.
	if buildFlutterSDK != "" {
		b, ferr := flutterBinFromSDK(buildFlutterSDK)
		if ferr != nil {
			return "", "", ferr
		}
		return b, "from --flutter-sdk", nil
	}

	// 2. Saved config.
	if cfg, cerr := config.Load(); cerr == nil && cfg.FlutterSDKPath != "" {
		b, ferr := flutterBinFromSDK(cfg.FlutterSDKPath)
		if ferr != nil {
			return "", "", ferr
		}
		return b, "from saved config", nil
	}

	// 3. PATH fallback, with a caveat.
	b, lerr := exec.LookPath("flutter")
	if lerr != nil {
		return "", "", fmt.Errorf(
			"no Flutter SDK configured and none on PATH.\n"+
				"Point Koolbase at a Flutter %s SDK:\n"+
				"  koolbase build macos --flutter-sdk /path/to/flutter-%s",
			flutterVersion, flutterVersion)
	}
	fmt.Printf("⚠ Using flutter from PATH. It MUST be version %s to match the engine,\n"+
		"  or the build will fail with Dart language-version errors. To pin a\n"+
		"  matching SDK: koolbase build macos --flutter-sdk /path/to/flutter-%s\n\n",
		flutterVersion, flutterVersion)
	return b, "from PATH (unverified version)", nil
}

// flutterBinFromSDK turns a Flutter SDK root into the path of its flutter
// binary, expanding a leading ~ and verifying the binary exists.
func flutterBinFromSDK(sdkRoot string) (string, error) {
	expanded, err := expandHome(sdkRoot)
	if err != nil {
		return "", err
	}
	// Accept either the SDK root (…/flutter) or a direct path to bin/flutter.
	candidate := expanded
	if filepath.Base(expanded) != "flutter" || isDir(expanded) {
		candidate = filepath.Join(expanded, "bin", "flutter")
	}
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("flutter binary not found at %s (check --flutter-sdk path)", candidate)
	}
	return candidate, nil
}

func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// resolveProjectEngine detects the Flutter version for the current project and
// maps it to an installed Koolbase engine version string. For now it inspects
// installed engines and, if exactly one is installed, uses it; otherwise it
// asks the user to pass --engine. A future version will parse the project's
// Flutter version from `flutter --version` and match automatically.
func resolveProjectEngine() (string, error) {
	installed, err := engine.ListInstalled()
	if err != nil {
		return "", err
	}
	if len(installed) == 0 {
		return "", fmt.Errorf("no Koolbase engine installed — run: koolbase engine install <flutter-version>")
	}
	if len(installed) == 1 {
		return installed[0], nil
	}
	return "", fmt.Errorf("multiple engines installed (%s) — choose one with --engine",
		strings.Join(installed, ", "))
}

func init() {
	buildCmd.Flags().BoolVar(&buildRelease, "release", false, "Build in release mode")
	buildCmd.Flags().StringVar(&buildVersion, "engine", "", "Engine version to use (e.g. 3.22.3-koolbase.1)")
	buildCmd.Flags().StringVar(&buildFlutterSDK, "flutter-sdk", "", "Path to a version-matched Flutter SDK (e.g. ~/flutter-3.22.3)")
	buildCmd.Flags().BoolVar(&buildTreeShake, "tree-shake-icons", false, "Enable icon tree-shaking (only works with an engine that bundles const_finder)")
}
