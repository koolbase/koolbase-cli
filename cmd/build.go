package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
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
	buildNoTreeShake bool
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
	if platform != "macos" && platform != "android" {
		return fmt.Errorf("platform %q not supported — use 'macos' or 'android'", platform)
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
			version, baseFlutterVersion(version))
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

	var localEngine, localEngineHost, flutterSubcmd string
	switch platform {
	case "android":
		localEngine = "android_release_arm64"
		localEngineHost = "host_release_arm64"
		flutterSubcmd = "apk"
	default: // macos
		localEngine = "mac_release_" + hostArch()
		localEngineHost = localEngine
		flutterSubcmd = "macos"
	}

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
	// Preflight: fail fast and legibly if the SDK version doesn't match the engine.
	wantFlutter := baseFlutterVersion(version)
	if verr := verifyFlutterVersion(flutterBin, wantFlutter); verr != nil {
		return verr
	}

	flutterArgs := []string{
		"build", flutterSubcmd,
		"--local-engine=" + localEngine,
		"--local-engine-host=" + localEngineHost,
		"--local-engine-src-path=" + srcPath,
	}
	if buildRelease {
		flutterArgs = append(flutterArgs, "--release")
	}

	// Koolbase engines ship the icon tree-shaker host tools (const_finder +
	// font-subset), so tree-shaking works by default and produces smaller
	// bundles. --no-tree-shake-icons opts out if a build ever needs it.
	if buildNoTreeShake {
		flutterArgs = append(flutterArgs, "--no-tree-shake-icons")
	}

	fmt.Printf("Building %s with Koolbase engine %s\n", platform, version)
	fmt.Printf("  Flutter SDK: %s (%s)\n", flutterBin, sdkSource)
	fmt.Printf("  flutter %s\n\n", strings.Join(flutterArgs, " "))

	build := exec.Command(flutterBin, flutterArgs...)
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	build.Stdin = os.Stdin
	if err := build.Run(); err != nil {
		return fmt.Errorf("flutter build failed: %w", err)
	}

	if platform == "android" {
		projectDir, _ := os.Getwd()
		apkPath := filepath.Join(projectDir, "build", "app", "outputs", "flutter-apk", "app-release.apk")
		fmt.Println("\n  Stamping build_id...")
		buildID, changed, serr := stampBuildIDIntoAssets(projectDir, apkPath)
		if serr != nil {
			return fmt.Errorf("stamp build_id: %w", serr)
		}
		fmt.Printf("  \u2713 build_id %s\n", buildID)
		if changed {
			fmt.Println("  Bundling build_id asset (one rebuild)...")
			rebuild := exec.Command(flutterBin, flutterArgs...)
			rebuild.Stdout = os.Stdout
			rebuild.Stderr = os.Stderr
			rebuild.Stdin = os.Stdin
			if rerr := rebuild.Run(); rerr != nil {
				return fmt.Errorf("rebuild after stamp failed: %w", rerr)
			}
			// build_id is stable across asset-only changes (verified), so no re-stamp.
		}
	}

	fmt.Println("\n\u2713 Build complete. This binary supports Koolbase Code Push.")
	return nil
}

// resolveFlutterBin returns the path to the flutter binary to use, plus a short
// human-readable description of where it came from. engineVersion is used only
// to render helpful guidance (e.g. "3.22.3" from "3.22.3-koolbase.1").
//
// Resolution order: --flutter-sdk flag, then saved config, then PATH. When
// falling back to PATH, a caveat is printed because PATH flutter is very likely
// the developer's day-to-day SDK, which usually won't match the engine version.
// baseFlutterVersion strips the Koolbase engine suffix "-koolbase.<rev>" from an
// engine version, yielding the underlying Flutter version. Handles any revision,
// e.g. "3.44.0-koolbase.2" -> "3.44.0", "3.32.0-koolbase.1" -> "3.32.0". A version
// with no suffix is returned unchanged.
func baseFlutterVersion(engineVersion string) string {
	if i := strings.Index(engineVersion, "-koolbase."); i >= 0 {
		return engineVersion[:i]
	}
	return engineVersion
}

func resolveFlutterBin(engineVersion string) (bin string, source string, err error) {
	flutterVersion := baseFlutterVersion(engineVersion)

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

// flutterVersionRe extracts the version from `flutter --version` output, whose
// first line looks like: "Flutter 3.44.0 • channel ... • ...".
var flutterVersionRe = regexp.MustCompile(`Flutter\s+(\d+\.\d+\.\d+)`)

// verifyFlutterVersion runs `flutterBin --version` and confirms it matches the
// engine's required Flutter version (e.g. "3.44.0"). Returns a clear, actionable
// error on mismatch so a wrong SDK fails FAST and legibly instead of deep inside
// the Dart front-end with a cryptic language-version error minutes later.
func verifyFlutterVersion(flutterBin, wantVersion string) error {
	out, err := exec.Command(flutterBin, "--version").CombinedOutput()
	if err != nil {
		// Don't hard-fail on a flaky --version invocation; warn and proceed.
		fmt.Printf("⚠ Could not run 'flutter --version' to verify SDK version: %v\n"+
			"  Proceeding, but the SDK MUST be Flutter %s.\n\n", err, wantVersion)
		return nil
	}
	m := flutterVersionRe.FindSubmatch(out)
	if m == nil {
		fmt.Printf("⚠ Could not parse Flutter version from 'flutter --version'.\n"+
			"  Proceeding, but the SDK MUST be Flutter %s.\n\n", wantVersion)
		return nil
	}
	got := string(m[1])
	if got != wantVersion {
		return fmt.Errorf(
			"Flutter SDK version mismatch.\n"+
				"  This Koolbase engine requires Flutter %s, but the resolved SDK is Flutter %s.\n"+
				"  Building with a mismatched SDK fails with confusing Dart language-version errors.\n"+
				"  Fix: install Flutter %s and point Koolbase at it:\n"+
				"    koolbase build android --release --flutter-sdk /path/to/flutter-%s\n"+
				"  (or set it once via your saved config).",
			wantVersion, got, wantVersion, wantVersion)
	}
	return nil
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
	buildCmd.Flags().BoolVar(&buildNoTreeShake, "no-tree-shake-icons", false, "Disable icon tree-shaking (keeps all icon glyphs; larger bundle)")
}
