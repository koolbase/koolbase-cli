package cmd

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var (
	releaseEngine              string
	releaseFlutterSDK          string
	releaseArchs               []string
	releaseNoTreeShake         bool
	releaseProject             string
	releaseNoRegister          bool
	releaseChannel             string
	releaseFlavor              string
	releaseDartDefines         []string
	releaseDartDefineFromFiles []string
)

// koolbaseBuildIDAsset is the AAB path of the stamped build_id asset. In a
// multi-ABI release we overwrite its content with a JSON map {abi: build_id}.
const koolbaseBuildIDAsset = "base/assets/flutter_assets/assets/koolbase_build_id"

// koolbaseFlutterVersionAsset is the AAB path of the stamped flutter_version
// asset. Unlike build_id it is a bare string (identical across ABIs), written
// authoritatively at assembly time from --engine.
const koolbaseFlutterVersionAsset = "base/assets/flutter_assets/assets/koolbase_flutter_version"

var releaseCmd = &cobra.Command{
	Use:   "release [platform] [-- <flutter flags>]",
	Short: "Build a shippable multi-ABI app bundle (AAB) with Koolbase Code Push",
	Long: `Assemble a single Android App Bundle (.aab) that carries the Koolbase
engine for every target ABI — the format new Google Play apps require.

Unlike 'koolbase build' (a single-ABI APK for local testing), 'release' builds
each ABI, stamps a per-ABI build_id map, and merges them into one .aab you
upload to Play (Play re-signs it).

Flavor, dart-defines and any other flutter flags are forwarded the same way as
'koolbase build' — --flavor / --dart-define / --dart-define-from-file are
first-class, everything else goes after a '--' separator:

  koolbase release android --flavor prod -- --build-name=1.2.3 --obfuscate --split-debug-info=build/symbols

Examples:
  koolbase release android --engine 3.44.0-koolbase.2 --flutter-sdk ~/flutter-3.44.0
  koolbase release android --target-archs arm64,arm --flavor prod
  koolbase release android --flavor prod --dart-define API_URL=https://api.example.com`,
	Args: oneArgBeforeDash,
	RunE: runRelease,
}

func runRelease(cmd *cobra.Command, args []string) error {
	platform := args[0]
	if platform != "android" {
		return fmt.Errorf("release currently supports only 'android' (iOS is post-launch, different mechanism)")
	}
	if len(releaseArchs) == 0 {
		return fmt.Errorf("no target ABIs — pass --target-archs (e.g. arm64,arm)")
	}

	// Everything after '--' is forwarded to flutter verbatim.
	var passthrough []string
	if d := cmd.ArgsLenAtDash(); d >= 0 {
		passthrough = append(passthrough, args[d:]...)
	}

	version := releaseEngine
	if version == "" {
		resolved, err := resolveProjectEngine()
		if err != nil {
			return err
		}
		version = resolved
	}

	// One version-matched Flutter SDK, shared by every ABI build + the shell.
	// resolveFlutterBin reads the buildFlutterSDK global; bridge our flag to it.
	buildFlutterSDK = releaseFlutterSDK
	flutterBin, sdkSource, err := resolveFlutterBin(version)
	if err != nil {
		return err
	}
	if verr := verifyFlutterVersion(flutterBin, baseFlutterVersion(version)); verr != nil {
		return verr
	}

	projectDir, _ := os.Getwd()
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate koolbase binary: %w", err)
	}

	// Outside build/ on purpose: each per-ABI build runs `flutter clean`,
	// which wipes build/ — the extracted libs must survive that.
	work, err := os.MkdirTemp("", "koolbase-release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	fmt.Printf("Koolbase release: engine %s, ABIs %s\n", version, strings.Join(releaseArchs, ", "))
	if releaseFlavor != "" {
		fmt.Printf("  Flavor: %s\n", releaseFlavor)
	}
	fmt.Printf("  Flutter SDK: %s (%s)\n\n", flutterBin, sdkSource)

	type abiArtifact struct {
		arch    string // CLI form: arm64 / arm
		abiDir  string // AAB lib dir: arm64-v8a / armeabi-v7a
		buildID string
		libDir  string
	}
	var arts []abiArtifact

	// 1. Build each ABI via the proven 'koolbase build android' path, then
	//    capture its stamped build_id and native libs. The inner build forwards
	//    flavor + defines + the FULL passthrough.
	for _, arch := range releaseArchs {
		fmt.Printf("=== building ABI: %s ===\n", arch)
		// Clean between ABIs so no prior-ABI artifacts leak into the APK.

		clean := exec.Command(flutterBin, "clean")
		clean.Stdout, clean.Stderr = os.Stdout, os.Stderr
		_ = clean.Run()
		// `flutter clean` wipes the generated assets/koolbase_build_id, which is
		// declared in pubspec. The inner build stamps it with the real build_id,
		// but flutter's asset bundler fails on the missing-but-declared asset
		// before the stamp runs. Ensure an empty placeholder exists post-clean;
		// the inner build overwrites it.
		bidPlaceholder := filepath.Join(projectDir, "assets", "koolbase_build_id")
		if err := os.MkdirAll(filepath.Dir(bidPlaceholder), 0o755); err != nil {
			return fmt.Errorf("create assets dir for %s: %w", arch, err)
		}
		if _, statErr := os.Stat(bidPlaceholder); os.IsNotExist(statErr) {
			if werr := os.WriteFile(bidPlaceholder, []byte(""), 0o644); werr != nil {
				return fmt.Errorf("create koolbase_build_id placeholder for %s: %w", arch, werr)
			}
		}
		// Same clean-race guard for the flutter_version asset: pubspec declares it
		// (the inner build's ensurePubspecAsset), but flutter clean wipes the file.
		// Seed the real value so the bundler does not fail; the inner build re-stamps it.
		fvPlaceholder := filepath.Join(projectDir, "assets", "koolbase_flutter_version")
		if _, statErr := os.Stat(fvPlaceholder); os.IsNotExist(statErr) {
			if werr := os.WriteFile(fvPlaceholder, []byte(baseFlutterVersion(version)+"\n"), 0o644); werr != nil {
				return fmt.Errorf("create koolbase_flutter_version placeholder for %s: %w", arch, werr)
			}
		}

		buildArgs := []string{"build", "android", "--engine", version, "--target-arch", arch, "--release"}
		if releaseFlutterSDK != "" {
			buildArgs = append(buildArgs, "--flutter-sdk", releaseFlutterSDK)
		}
		if releaseNoTreeShake {
			buildArgs = append(buildArgs, "--no-tree-shake-icons")
		}
		if releaseFlavor != "" {
			buildArgs = append(buildArgs, "--flavor", releaseFlavor)
		}
		for _, d := range releaseDartDefines {
			buildArgs = append(buildArgs, "--dart-define="+d)
		}
		for _, f := range releaseDartDefineFromFiles {
			buildArgs = append(buildArgs, "--dart-define-from-file="+f)
		}
		// Inner builds carry the full passthrough verbatim (obfuscate,
		// split-debug-info, target, …). Re-insert '--' so the inner koolbase
		// command treats them as passthrough, not as its own flags.
		if len(passthrough) > 0 {
			buildArgs = append(buildArgs, "--")
			buildArgs = append(buildArgs, passthrough...)
		}

		var bout bytes.Buffer
		b := exec.Command(self, buildArgs...)
		b.Stdout = io.MultiWriter(os.Stdout, &bout)
		b.Stderr, b.Stdin = os.Stderr, os.Stdin
		if err := b.Run(); err != nil {
			return fmt.Errorf("build ABI %s failed: %w", arch, err)
		}

		abiDir := androidABIDir(arch)
		bidPath := filepath.Join(projectDir, "assets", "koolbase_build_id")
		raw, rerr := os.ReadFile(bidPath)
		if rerr != nil {
			return fmt.Errorf("read stamped build_id for %s: %w", arch, rerr)
		}
		buildID := strings.TrimSpace(string(raw))

		// APK path comes from the build's own KOOLBASE_ARTIFACT line, not a
		// hardcoded literal — so it's correct under any --flavor.
		apk, perr := parseKoolbaseArtifact(bout.String())
		if perr != nil {
			return fmt.Errorf("locate built APK for %s: %w", arch, perr)
		}

		libDir := filepath.Join(work, abiDir)
		if err := os.MkdirAll(libDir, 0o755); err != nil {
			return err
		}
		if err := extractLibsFromAPK(apk, abiDir, libDir); err != nil {
			return fmt.Errorf("extract libs for %s: %w", arch, err)
		}
		fmt.Printf("  \u2713 %s build_id %s\n\n", abiDir, buildID)
		arts = append(arts, abiArtifact{arch: arch, abiDir: abiDir, buildID: buildID, libDir: libDir})
	}

	// 2. Stock app-bundle SHELL at the matching Flutter version: supplies the
	//    AAB structure (dex, manifest, assets, manifest-registered build_id
	//    asset). Its native libs get swapped for the Koolbase ones below.
	//
	//    The shell carries --flavor (drives the gradle variant + AAB path) and
	//    the passthrough MINUS {--obfuscate, --split-debug-info}: the shell's
	//    libapp is discarded, so it never needs obfuscating, and an obfuscated
	//    shell would overwrite the REAL per-ABI symbol maps. It does NOT carry
	//    dart-defines (they only shape the discarded libapp, nothing that ships).
	tps := make([]string, 0, len(arts))
	for _, a := range arts {
		if a.arch == "arm" || a.arch == "armeabi-v7a" {
			tps = append(tps, "android-arm")
		} else {
			tps = append(tps, "android-arm64")
		}
	}
	fmt.Println("=== building app bundle shell ===")
	shellArgs := []string{"build", "appbundle", "--release", "--target-platform=" + strings.Join(tps, ",")}
	if releaseFlavor != "" {
		shellArgs = append(shellArgs, "--flavor", releaseFlavor)
	}
	shellArgs = append(shellArgs, stripShellDenylist(passthrough)...)

	var shellOut bytes.Buffer
	sh := exec.Command(flutterBin, shellArgs...)
	sh.Stdout = io.MultiWriter(os.Stdout, &shellOut)
	sh.Stderr, sh.Stdin = os.Stderr, os.Stdin
	if err := sh.Run(); err != nil {
		return fmt.Errorf("app bundle shell build failed: %w", err)
	}
	// Shell AAB path from flutter's own "✓ Built" line, not a hardcoded literal.
	shellAAB, serr := resolveBuiltArtifact(shellOut.String(), projectDir, "appbundle", releaseFlavor, true)
	if serr != nil {
		return serr
	}

	// 3. Assemble: swap Koolbase libs + write the per-ABI build_id map.
	outDir := filepath.Join(projectDir, "build", "app", "outputs", "koolbase")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// Mirror the shell's (flavor-shaped) filename so flavored releases don't
	// collide, e.g. app-prod-release.aab.
	outAAB := filepath.Join(outDir, filepath.Base(shellAAB))

	buildIDMap := make(map[string]string, len(arts))
	libSwaps := make(map[string]string)
	engined := make(map[string]bool)
	for _, a := range arts {
		buildIDMap[a.abiDir] = a.buildID
		engined[a.abiDir] = true
		libSwaps["base/lib/"+a.abiDir+"/libflutter.so"] = filepath.Join(a.libDir, "libflutter.so")
		libSwaps["base/lib/"+a.abiDir+"/libapp.so"] = filepath.Join(a.libDir, "libapp.so")
	}
	mapJSON, _ := json.Marshal(buildIDMap)

	extraAbis, err := assembleReleaseAAB(shellAAB, outAAB, libSwaps, string(mapJSON), baseFlutterVersion(version), engined)
	if err != nil {
		return fmt.Errorf("assemble AAB: %w", err)
	}

	if serr := signReleaseAAB(outAAB, projectDir); serr != nil {
		return fmt.Errorf("sign AAB: %w", serr)
	}

	fmt.Printf("\n\u2713 Release bundle ready: %s\n", outAAB)

	for abi, bid := range buildIDMap {
		fmt.Printf("    %-13s build_id %s\n", abi, bid)
	}

	// Register a build_id release per ABI so the resolver can serve patches to
	// each ABI's devices. CreateRelease is idempotent server-side.
	cfg, cerr := config.Load()
	projectID := releaseProject
	if cerr == nil && projectID == "" {
		projectID = cfg.ProjectID
	}
	if cerr != nil || projectID == "" || cfg.APIKey == "" {
		if !releaseNoRegister {
			return fmt.Errorf("releases NOT registered (no --project and no saved login) — this AAB would be UNPATCHABLE.\n" +
				"  Code Push cannot serve patches to these build_ids until they are registered.\n" +
				"  Fix: run `koolbase login` and/or pass --project <id>, then rebuild.\n" +
				"  If you intend an unregistered build (local testing / CI), pass --no-register to bypass this check.")
		}
		fmt.Println("\nWARNING: releases NOT registered (--no-register set).")
		fmt.Println("  This AAB is UNPATCHABLE — Code Push cannot serve patches to these build_ids.")
	} else {
		appVersion := readPubspecVersion(projectDir)
		if appVersion == "" {
			fmt.Fprintln(os.Stderr, "  ⚠ registering releases without app_version — no version: found in pubspec.yaml; clients can't gate patches by store version")
		}
		apiClient := api.NewClient(cfg.BaseURL, cfg.APIKey)
		// Same fingerprint for every ABI (identical flag set); computed once.
		buildConfig := buildFingerprint(releaseFlavor, releaseDartDefines, releaseDartDefineFromFiles, passthrough)
		fmt.Println("\n=== registering releases ===")
		for _, a := range arts {
			rel, rerr := apiClient.CreateRelease(projectID, api.CreateReleaseRequest{
				BuildID:        a.buildID,
				FlutterVersion: baseFlutterVersion(version),
				Platform:       "android",
				AppVersion:     appVersion,
				MatchMode:      "build_id",
				Channel:        releaseChannel,
				BuildConfig:    buildConfig,
			})
			if rerr != nil {
				return fmt.Errorf("register release for %s (build_id %s): %w", a.abiDir, a.buildID, rerr)
			}
			fmt.Printf("  registered %s -> release %s (build_id %s, channel %s)\n", a.abiDir, rel.ID, a.buildID, releaseChannel)

			// Upload the base libapp.so so patches can be pushed later WITHOUT
			// manually extracting the base from the AAB. Non-fatal: if any step
			// fails, the release is still registered and remains patchable the
			// old way (patch push --binary <base>).
			basePath := filepath.Join(a.libDir, "libapp.so")
			up, uerr := apiClient.GetReleaseBaseUploadURL(projectID, rel.ID, a.abiDir)
			if uerr != nil {
				fmt.Printf("  ⚠ base upload skipped for %s (could not get upload url): %v\n", a.abiDir, uerr)
				continue
			}
			if perr := apiClient.PutBaseArtifact(up.UploadURL, basePath); perr != nil {
				fmt.Printf("  ⚠ base upload failed for %s: %v\n", a.abiDir, perr)
				continue
			}
			if cerr := apiClient.ConfirmReleaseBase(projectID, rel.ID, up.StorageKey); cerr != nil {
				fmt.Printf("  ⚠ base upload not recorded for %s: %v\n", a.abiDir, cerr)
				continue
			}
			fmt.Printf("  ✓ base stored for %s (patch push won't need --binary)\n", a.abiDir)
		}
	}
	if len(extraAbis) > 0 {
		fmt.Printf("\n\u26a0 %s present (plugin libs) but Koolbase has no engine for %s yet.\n",
			strings.Join(extraAbis, ", "), strings.Join(extraAbis, "/"))
		fmt.Println("  Those devices would get a split with no Koolbase engine. Before a Play")
		fmt.Println("  upload, restrict ABIs in android/app/build.gradle, e.g.:")
		fmt.Println("      android { defaultConfig { ndk { abiFilters " +
			abiFiltersList(arts2dirs(buildIDMap)) + " } } }")
	}
	fmt.Println("\n  Upload this .aab to Google Play (Play re-signs it).")
	return nil
}

// parseKoolbaseArtifact pulls the artifact path out of the structured
// "KOOLBASE_ARTIFACT type=… abi=… path=…" line a `koolbase build` subprocess
// emits. path= is last on the line, so the path may contain spaces. Last match
// wins (a build may emit one line per invocation).
var koolbaseArtifactRe = regexp.MustCompile(`(?m)^KOOLBASE_ARTIFACT\b.*?\bpath=(.+?)\s*$`)

func parseKoolbaseArtifact(stdout string) (string, error) {
	ms := koolbaseArtifactRe.FindAllStringSubmatch(stdout, -1)
	if len(ms) == 0 {
		return "", fmt.Errorf("no KOOLBASE_ARTIFACT line in build output")
	}
	return strings.TrimSpace(ms[len(ms)-1][1]), nil
}

// stripShellDenylist removes --obfuscate and --split-debug-info (both joined and
// space-separated forms, value token included) from a passthrough slice bound for
// the app-bundle shell. The shell's libapp.so is discarded, so it never needs
// obfuscating — and an obfuscated shell would overwrite the REAL per-ABI symbol
// maps in the shared --split-debug-info dir; dropping --split-debug-info means the
// shell writes no maps, so the per-ABI ones survive untouched. flutter also errors
// if --obfuscate is set without --split-debug-info, so they're dropped as a pair.
// Everything else (--target, --build-name, --build-number, …) passes through.
func stripShellDenylist(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--obfuscate" {
			continue
		}
		if a == "--split-debug-info" {
			// space-separated form: drop this flag AND its value token.
			if i+1 < len(args) {
				i++
			}
			continue
		}
		if strings.HasPrefix(a, "--split-debug-info=") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// extractLibsFromAPK pulls lib/<abiDir>/libflutter.so and libapp.so from the
// built APK into dest.
func extractLibsFromAPK(apkPath, abiDir, dest string) error {
	zr, err := zip.OpenReader(apkPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	want := map[string]string{
		"lib/" + abiDir + "/libflutter.so": filepath.Join(dest, "libflutter.so"),
		"lib/" + abiDir + "/libapp.so":     filepath.Join(dest, "libapp.so"),
	}
	found := 0
	for _, f := range zr.File {
		out, ok := want[f.Name]
		if !ok {
			continue
		}
		if err := copyZipEntryToFile(f, out); err != nil {
			return err
		}
		found++
	}
	if found != len(want) {
		return fmt.Errorf("expected %d libs for %s in %s, found %d",
			len(want), abiDir, filepath.Base(apkPath), found)
	}
	return nil
}

func copyZipEntryToFile(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

// assembleReleaseAAB copies the shell AAB into outPath, swapping in the Koolbase
// per-ABI libs and overwriting koolbase_build_id with the build_id map. It drops
// META-INF (stale signature; Play re-signs) and writes no directory entries
// (bundletool rejects them). Returns any non-engined ABI dirs found in the
// bundle (e.g. x86_64 plugin libs) so the caller can warn.
func assembleReleaseAAB(shellPath, outPath string, libSwaps map[string]string, buildIDMapJSON, flutterVersion string, engined map[string]bool) ([]string, error) {
	zr, err := zip.OpenReader(shellPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	outf, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	defer outf.Close()
	zw := zip.NewWriter(outf)
	defer zw.Close()

	writeData := func(name string, data []byte) error {
		w, werr := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if werr != nil {
			return werr
		}
		_, werr = w.Write(data)
		return werr
	}

	wroteMap := false
	wroteFV := false
	extra := map[string]bool{}
	for _, f := range zr.File {
		name := f.Name
		if strings.HasPrefix(name, "META-INF/") {
			continue
		}
		if strings.HasSuffix(name, "/") {
			continue
		}
		// Track non-engined native ABIs present (e.g. x86_64 plugin libs).
		if strings.HasPrefix(name, "base/lib/") {
			parts := strings.SplitN(strings.TrimPrefix(name, "base/lib/"), "/", 2)
			if len(parts) == 2 && !engined[parts[0]] {
				extra[parts[0]] = true
			}
		}
		if name == koolbaseBuildIDAsset {
			if err := writeData(name, []byte(buildIDMapJSON)); err != nil {
				return nil, err
			}
			wroteMap = true
			continue
		}
		if name == koolbaseFlutterVersionAsset {
			if err := writeData(name, []byte(flutterVersion+"\n")); err != nil {
				return nil, err
			}
			wroteFV = true
			continue
		}
		if src, ok := libSwaps[name]; ok {
			data, rerr := os.ReadFile(src)
			if rerr != nil {
				return nil, rerr
			}
			if err := writeData(name, data); err != nil {
				return nil, err
			}
			continue
		}
		rc, oerr := f.Open()
		if oerr != nil {
			return nil, oerr
		}
		w, cerr := zw.CreateHeader(&zip.FileHeader{Name: name, Method: f.Method})
		if cerr != nil {
			rc.Close()
			return nil, cerr
		}
		if _, err := io.Copy(w, rc); err != nil {
			rc.Close()
			return nil, err
		}
		rc.Close()
	}
	if !wroteMap {
		return nil, fmt.Errorf("koolbase_build_id asset not present in shell AAB — the per-ABI build must run stampBuildIDIntoAssets (it declares the asset)")
	}
	// flutter_version rides in from the inner builds; if an older shell AAB lacks
	// it, inject so the AAB always carries the engine version for the resolver.
	if !wroteFV {
		if err := writeData(koolbaseFlutterVersionAsset, []byte(flutterVersion+"\n")); err != nil {
			return nil, err
		}
	}
	var out []string
	for a := range extra {
		out = append(out, a)
	}
	return out, nil
}

// signReleaseAAB signs the merged AAB in place with the upload key from
// android/key.properties — the same key gradle uses for the shell. The merge
// step re-zips the bundle, which strips gradle's signature, so without this the
// AAB uploads to Play as "unsigned".
func signReleaseAAB(aabPath, projectDir string) error {
	propsPath := filepath.Join(projectDir, "android", "key.properties")
	data, rerr := os.ReadFile(propsPath)
	if rerr != nil {
		fmt.Println("\nWARNING: android/key.properties not found — the merged AAB is UNSIGNED.")
		fmt.Println("  Sign it before uploading to Play, or add key.properties so release can sign it.")
		return nil
	}

	props := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		props[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}

	storeFile := props["storeFile"]
	keyAlias := props["keyAlias"]
	if storeFile == "" || keyAlias == "" {
		return fmt.Errorf("key.properties missing storeFile or keyAlias")
	}
	if !filepath.IsAbs(storeFile) {
		// gradle resolves storeFile via file() relative to the :app module dir.
		storeFile = filepath.Join(projectDir, "android", "app", storeFile)
	}
	if _, serr := os.Stat(storeFile); serr != nil {
		return fmt.Errorf("keystore not found at %s: %w", storeFile, serr)
	}

	jarsigner := "jarsigner"
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		cand := filepath.Join(jh, "bin", "jarsigner")
		if _, serr := os.Stat(cand); serr == nil {
			jarsigner = cand
		}
	}

	// Pass passwords via env, not argv, so they don't appear in `ps`.
	args := []string{"-keystore", storeFile, "-storepass:env", "KB_STOREPASS"}
	env := append(os.Environ(), "KB_STOREPASS="+props["storePassword"])
	if kp := props["keyPassword"]; kp != "" {
		args = append(args, "-keypass:env", "KB_KEYPASS")
		env = append(env, "KB_KEYPASS="+kp)
	}
	args = append(args, aabPath, keyAlias)

	signCmd := exec.Command(jarsigner, args...)
	signCmd.Env = env
	if out, serr := signCmd.CombinedOutput(); serr != nil {
		return fmt.Errorf("jarsigner failed: %w\n%s", serr, out)
	}
	if out, serr := exec.Command(jarsigner, "-verify", aabPath).CombinedOutput(); serr != nil {
		return fmt.Errorf("jarsigner -verify failed: %w\n%s", serr, out)
	}
	fmt.Println("  \u2713 AAB signed with upload key")
	return nil
}

func arts2dirs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func abiFiltersList(dirs []string) string {
	q := make([]string, 0, len(dirs))
	for _, d := range dirs {
		q = append(q, "'"+d+"'")
	}
	return strings.Join(q, ", ")
}

// readPubspecVersion returns the project's pubspec version (e.g. "1.0.0+1"),
// used as the release app_version for grouping. Empty if not found.
func readPubspecVersion(projectDir string) string {
	data, err := os.ReadFile(filepath.Join(projectDir, "pubspec.yaml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "version:") {
			return strings.TrimSpace(strings.TrimPrefix(t, "version:"))
		}
	}
	return ""
}

func init() {
	releaseCmd.Flags().StringVar(&releaseEngine, "engine", "", "Engine version to use (e.g. 3.44.0-koolbase.2)")
	releaseCmd.Flags().StringVar(&releaseFlutterSDK, "flutter-sdk", "", "Path to a version-matched Flutter SDK")
	releaseCmd.Flags().StringSliceVar(&releaseArchs, "target-archs", []string{"arm64", "arm"}, "Target ABIs (comma-separated): arm64,arm")
	releaseCmd.Flags().BoolVar(&releaseNoTreeShake, "no-tree-shake-icons", false, "Disable icon tree-shaking")
	releaseCmd.Flags().StringVar(&releaseProject, "project", "", "Koolbase project/app ID (defaults to saved config)")
	releaseCmd.Flags().BoolVar(&releaseNoRegister, "no-register", false, "Build without registering build_ids (produces an UNPATCHABLE AAB; for local testing/CI only)")
	releaseCmd.Flags().StringVar(&releaseChannel, "channel", "stable", "Release channel for the registered releases")
	releaseCmd.Flags().StringVar(&releaseFlavor, "flavor", "", "Build flavor (e.g. prod); selects the gradle product flavor and shapes output paths")
	releaseCmd.Flags().StringArrayVar(&releaseDartDefines, "dart-define", nil, "Dart environment value as KEY=VALUE (repeatable)")
	releaseCmd.Flags().StringArrayVar(&releaseDartDefineFromFiles, "dart-define-from-file", nil, "Load --dart-define values from a JSON or .env file (repeatable)")
	rootCmd.AddCommand(releaseCmd)
}
