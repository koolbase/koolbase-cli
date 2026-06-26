package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/kennedyowusu/koolbase-cli/internal/engine"
	"github.com/spf13/cobra"
)

var (
	buildRelease             bool
	buildVersion             string
	buildFlutterSDK          string
	buildNoTreeShake         bool
	buildTargetArch          string
	buildFlavor              string
	buildDartDefines         []string
	buildDartDefineFromFiles []string
)

var buildCmd = &cobra.Command{
	Use:   "build [platform] [-- <flutter flags>]",
	Short: "Build your Flutter app with the Koolbase engine (enables Code Push)",
	Long: `Build a Flutter app using the installed Koolbase engine so the result
supports Code Push.

This wraps 'flutter build' with the correct --local-engine flags pointing at
the Koolbase engine you installed via 'koolbase engine install'. Koolbase owns
only engine selection, the --local-engine wiring, --target-platform and build_id
stamping; everything that shapes the app itself — flavor, dart-defines, the
entrypoint, obfuscation — is yours and is forwarded straight to flutter.

First-class flags Koolbase acts on or that carry a parsing footgun:
  --flavor                 selects the gradle product flavor (also renames outputs)
  --dart-define KEY=VALUE  repeatable; values may contain commas / URLs / JSON
  --dart-define-from-file  repeatable

Anything else flutter accepts — --target, --build-name, --build-number,
--obfuscate, --split-debug-info, … — goes after a '--' separator and is passed
through untouched:

  koolbase build android --release --flavor prod -- --build-name=1.2.3 --obfuscate --split-debug-info=build/symbols

IMPORTANT: the Flutter SDK version must match the engine's Flutter version
(e.g. an engine built for Flutter 3.44.0 needs the 3.44.0 SDK). Point Koolbase
at the matching SDK with --flutter-sdk, or save it once.

Examples:
  koolbase build android --release --flutter-sdk ~/flutter-3.44.0
  koolbase build android --release --target-arch arm --flavor prod
  koolbase build android --release --flavor prod --dart-define API_URL=https://api.example.com
  koolbase build macos  --release --engine 3.22.3-koolbase.1`,
	Args: oneArgBeforeDash,
	RunE: runBuild,
}

func runBuild(cmd *cobra.Command, args []string) error {
	platform := args[0]
	if platform != "macos" && platform != "android" {
		return fmt.Errorf("platform %q not supported — use 'macos' or 'android'", platform)
	}

	// Everything after '--' is forwarded to flutter verbatim (e.g. --target,
	// --build-name, --build-number, --obfuscate, --split-debug-info).
	var passthrough []string
	if d := cmd.ArgsLenAtDash(); d >= 0 {
		passthrough = append(passthrough, args[d:]...)
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

	var (
		installed bool
		err       error
	)
	if platform == "android" {
		androidCfg, _, _ := androidEngineConfig(buildTargetArch)
		installed, err = engine.IsInstalledArch(version, androidCfg)
	} else {
		installed, err = engine.IsInstalled(version)
	}
	if err != nil {
		return err
	}
	if !installed {
		msg := fmt.Sprintf("engine %s is not installed for this target — run: koolbase engine install %s",
			version, baseFlutterVersion(version))
		if platform == "android" && buildTargetArch != "" && buildTargetArch != "arm64" {
			msg += " --target-arch " + buildTargetArch
		}
		return fmt.Errorf("%s", msg)
	}

	engineDir, err := engine.VersionDir(version)
	if err != nil {
		return err
	}

	// The engine archive lays out as {version}/src/out/<config>.
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
		cfg, _, ok := androidEngineConfig(buildTargetArch)
		if !ok {
			return fmt.Errorf("unsupported --target-arch %q — use 'arm64' or 'arm'", buildTargetArch)
		}
		localEngine = cfg
		localEngineHost = "host_release_arm64"
		flutterSubcmd = "apk"
	default: // macos
		localEngine = "mac_release_" + hostArch()
		localEngineHost = localEngine
		flutterSubcmd = "macos"
	}

	// Resolve the flutter binary. The SDK version MUST match the engine's
	// Flutter version or Dart compilation fails. Resolution order:
	//   1. --flutter-sdk flag  2. saved config  3. flutter on PATH (with caveat).
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
	// Restrict the APK to the single target ABI so it matches the single-ABI
	// local engine. Otherwise plugin .so for other ABIs make Android pick a
	// primaryCpuAbi (e.g. arm64) with no matching libflutter.so → dlopen crash.
	if platform == "android" {
		tp := "android-arm64"
		if buildTargetArch == "arm" || buildTargetArch == "armeabi-v7a" {
			tp = "android-arm"
		}
		flutterArgs = append(flutterArgs, "--target-platform="+tp)
	}
	if buildRelease {
		flutterArgs = append(flutterArgs, "--release")
	}
	// Koolbase engines ship the icon tree-shaker host tools, so tree-shaking
	// works by default. --no-tree-shake-icons opts out if a build needs it.
	if buildNoTreeShake {
		flutterArgs = append(flutterArgs, "--no-tree-shake-icons")
	}

	// Developer's app-shaping flags, forwarded straight to flutter. --flavor is
	// first-class because Koolbase derives the artifact path from it; dart-defines
	// are first-class because StringArray avoids the comma-split corruption a
	// raw --dart-define would suffer. Everything else rides the '--' passthrough.
	if buildFlavor != "" {
		flutterArgs = append(flutterArgs, "--flavor", buildFlavor)
	}
	for _, d := range buildDartDefines {
		flutterArgs = append(flutterArgs, "--dart-define="+d)
	}
	for _, f := range buildDartDefineFromFiles {
		flutterArgs = append(flutterArgs, "--dart-define-from-file="+f)
	}
	flutterArgs = append(flutterArgs, passthrough...)

	fmt.Printf("Building %s with Koolbase engine %s\n", platform, version)
	fmt.Printf("  Flutter SDK: %s (%s)\n", flutterBin, sdkSource)
	fmt.Printf("  flutter %s\n\n", strings.Join(flutterArgs, " "))

	projectDir, _ := os.Getwd()

	// Tee flutter's stdout so the real (flavor-shaped) artifact path is read from
	// flutter's own "✓ Built <path>" line rather than a hardcoded literal.
	var buildOut bytes.Buffer
	build := exec.Command(flutterBin, flutterArgs...)
	build.Stdout = io.MultiWriter(os.Stdout, &buildOut)
	build.Stderr = os.Stderr
	build.Stdin = os.Stdin
	if err := build.Run(); err != nil {
		return fmt.Errorf("flutter build failed: %w", err)
	}

	artifactPath, rerr := resolveBuiltArtifact(buildOut.String(), projectDir, flutterSubcmd, buildFlavor, buildRelease)
	if rerr != nil {
		return rerr
	}

	// flutter_version is platform-agnostic and known up front (from --engine), so
	// stamp it on EVERY build — the SDK reports it on patch-check so the resolver
	// refuses a patch built on a different engine. Folded into the same single
	// rebuild as build_id below when both change, so there is at most one rebuild.
	fvChanged, fverr := stampFlutterVersionIntoAssets(projectDir, version)
	if fverr != nil {
		return fmt.Errorf("stamp flutter_version: %w", fverr)
	}
	fmt.Printf("  \u2713 flutter_version %s\n", baseFlutterVersion(version))

	needRebuild := fvChanged
	if platform == "android" {
		fmt.Println("\n  Stamping build_id...")
		buildID, changed, serr := stampBuildIDIntoAssets(projectDir, artifactPath, androidABIDir(buildTargetArch))
		if serr != nil {
			return fmt.Errorf("stamp build_id: %w", serr)
		}
		fmt.Printf("  \u2713 build_id %s\n", buildID)
		needRebuild = needRebuild || changed
	}
	if needRebuild {
		fmt.Println("  Bundling Koolbase assets (one rebuild)...")
		// The rebuild replays the FULL flag set (flavor + defines + passthrough),
		// so the bundled artifact differs only by the added asset(s).
		rebuild := exec.Command(flutterBin, flutterArgs...)
		rebuild.Stdout = os.Stdout
		rebuild.Stderr = os.Stderr
		rebuild.Stdin = os.Stdin
		if rerr := rebuild.Run(); rerr != nil {
			return fmt.Errorf("rebuild after stamp failed: %w", rerr)
		}
		// Both build_id and flutter_version are stable across asset-only changes
		// (build_id verified on-device; flutter_version is a static string), and the
		// output path is unchanged, so artifactPath still points at the artifact.
	}

	artType := "apk"
	abiTag := buildTargetArch
	if platform == "macos" {
		artType = "app"
		abiTag = hostArch()
	}
	// Structured, machine-readable line consumed by `koolbase release` across the
	// subprocess boundary. path= is intentionally last so values containing
	// spaces survive a simple "everything after path=" parse.
	fmt.Printf("KOOLBASE_ARTIFACT type=%s abi=%s path=%s\n", artType, abiTag, artifactPath)

	fmt.Println("\n\u2713 Build complete. This binary supports Koolbase Code Push.")
	return nil
}

// oneArgBeforeDash validates that exactly one positional arg (the platform)
// precedes any '--' passthrough, so `koolbase build android -- --flag` parses
// the platform correctly while everything after '--' is forwarded untouched.
// Shared by `build` and `release`.
func oneArgBeforeDash(cmd *cobra.Command, args []string) error {
	n := cmd.ArgsLenAtDash()
	if n < 0 {
		n = len(args)
	}
	if n != 1 {
		return fmt.Errorf("expected exactly one platform argument before any '--' passthrough")
	}
	return nil
}

// builtArtifactRe extracts the artifact path from flutter's "✓ Built <path>"
// summary line, e.g. "✓ Built build/app/outputs/flutter-apk/app-prod-release.apk (8.4MB)".
// Anchoring on the known extension makes it robust to the optional trailing size
// (and its trailing period) and to wording drift across Flutter versions.
var builtArtifactRe = regexp.MustCompile(`(?m)✓\s+Built\s+(\S.*?\.(?:apk|aab|app|ipa))\b`)

// resolveBuiltArtifact returns the absolute path of the artifact flutter just
// produced. The source of truth is flutter's own "✓ Built <path>" line (captured
// from stdout) so the flavor-shaped output path is never hardcoded. If that line
// can't be parsed, it falls back to the conventional path for (subcmd, flavor,
// mode) and finally to a glob.
func resolveBuiltArtifact(stdout, projectDir, subcmd, flavor string, release bool) (string, error) {
	// 1. Flutter's reported path (last match wins — covers the post-stamp rebuild).
	if ms := builtArtifactRe.FindAllStringSubmatch(stdout, -1); len(ms) > 0 {
		p := strings.TrimSpace(ms[len(ms)-1][1])
		if !filepath.IsAbs(p) {
			p = filepath.Join(projectDir, p)
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// 2. Conventional path for this (subcmd, flavor, mode).
	if conv := conventionalArtifactPath(projectDir, subcmd, flavor, release); conv != "" {
		if _, err := os.Stat(conv); err == nil {
			return conv, nil
		}
	}
	// 3. Glob fallback.
	if g := globArtifact(projectDir, subcmd, flavor, release); g != "" {
		return g, nil
	}
	return "", fmt.Errorf("could not locate the built %s artifact: no parseable '✓ Built' line and nothing at the conventional path", subcmd)
}

// conventionalArtifactPath returns flutter's documented output path for a given
// build subcommand, flavor and mode. "" when there is no stable convention
// (macos product names vary — the glob handles those).
func conventionalArtifactPath(projectDir, subcmd, flavor string, release bool) string {
	mode := "release"
	if !release {
		mode = "debug"
	}
	switch subcmd {
	case "apk":
		name := "app-" + mode + ".apk"
		if flavor != "" {
			name = "app-" + flavor + "-" + mode + ".apk"
		}
		return filepath.Join(projectDir, "build", "app", "outputs", "flutter-apk", name)
	case "appbundle":
		variant := mode
		name := "app-" + mode + ".aab"
		if flavor != "" {
			capMode := strings.ToUpper(mode[:1]) + mode[1:]
			variant = flavor + capMode // e.g. prodRelease
			name = "app-" + flavor + "-" + mode + ".aab"
		}
		return filepath.Join(projectDir, "build", "app", "outputs", "bundle", variant, name)
	default:
		return ""
	}
}

// globArtifact is the last-resort locator: it globs the output dir and prefers a
// file matching the (flavor, mode) prefix, falling back to a sole match.
func globArtifact(projectDir, subcmd, flavor string, release bool) string {
	var pattern string
	switch subcmd {
	case "apk":
		pattern = filepath.Join(projectDir, "build", "app", "outputs", "flutter-apk", "app-*.apk")
	case "appbundle":
		pattern = filepath.Join(projectDir, "build", "app", "outputs", "bundle", "*", "app-*.aab")
	case "macos":
		pattern = filepath.Join(projectDir, "build", "macos", "Build", "Products", "Release", "*.app")
	default:
		return ""
	}
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return ""
	}
	if subcmd == "macos" {
		return matches[0]
	}
	mode := "release"
	if !release {
		mode = "debug"
	}
	want := "app-" + mode
	if flavor != "" {
		want = "app-" + flavor + "-" + mode
	}
	for _, m := range matches {
		if strings.HasPrefix(filepath.Base(m), want) {
			return m
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

// baseFlutterVersion strips the Koolbase engine suffix "-koolbase.<rev>" from an
// engine version, yielding the underlying Flutter version. A version with no
// suffix is returned unchanged.
func baseFlutterVersion(engineVersion string) string {
	if i := strings.Index(engineVersion, "-koolbase."); i >= 0 {
		return engineVersion[:i]
	}
	return engineVersion
}

// resolveFlutterBin returns the path to the flutter binary to use, plus a short
// description of where it came from. Resolution order: --flutter-sdk flag, saved
// config, then PATH (with a mismatch caveat printed).
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
// engine's required Flutter version, failing FAST and legibly on mismatch.
func verifyFlutterVersion(flutterBin, wantVersion string) error {
	out, err := exec.Command(flutterBin, "--version").CombinedOutput()
	if err != nil {
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

// resolveProjectEngine maps the current project to an installed Koolbase engine.
// For now: if exactly one engine is installed, use it; otherwise require --engine.
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
	buildCmd.Flags().StringVar(&buildFlutterSDK, "flutter-sdk", "", "Path to a version-matched Flutter SDK (e.g. ~/flutter-3.44.0)")
	buildCmd.Flags().BoolVar(&buildNoTreeShake, "no-tree-shake-icons", false, "Disable icon tree-shaking (keeps all icon glyphs; larger bundle)")
	buildCmd.Flags().StringVar(&buildTargetArch, "target-arch", "arm64", "Android target ABI: arm64 (arm64-v8a, default) or arm (armeabi-v7a)")
	buildCmd.Flags().StringVar(&buildFlavor, "flavor", "", "Build flavor (e.g. prod); selects the gradle product flavor and renames outputs to app-<flavor>-release.*")
	buildCmd.Flags().StringArrayVar(&buildDartDefines, "dart-define", nil, "Dart environment value as KEY=VALUE (repeatable; values may contain commas, URLs, JSON)")
	buildCmd.Flags().StringArrayVar(&buildDartDefineFromFiles, "dart-define-from-file", nil, "Load --dart-define values from a JSON or .env file (repeatable)")
}
