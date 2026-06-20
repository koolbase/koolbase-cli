package cmd

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var (
	releaseEngine      string
	releaseFlutterSDK  string
	releaseArchs       []string
	releaseNoTreeShake bool
	releaseProject     string
	releaseChannel     string
)

// koolbaseBuildIDAsset is the AAB path of the stamped build_id asset. In a
// multi-ABI release we overwrite its content with a JSON map {abi: build_id}.
const koolbaseBuildIDAsset = "base/assets/flutter_assets/assets/koolbase_build_id"

var releaseCmd = &cobra.Command{
	Use:   "release [platform]",
	Short: "Build a shippable multi-ABI app bundle (AAB) with Koolbase Code Push",
	Long: `Assemble a single Android App Bundle (.aab) that carries the Koolbase
engine for every target ABI — the format new Google Play apps require.

Unlike 'koolbase build' (a single-ABI APK for local testing), 'release' builds
each ABI, stamps a per-ABI build_id map, and merges them into one .aab you
upload to Play (Play re-signs it).

Examples:
  koolbase release android --engine 3.44.0-koolbase.2 --flutter-sdk ~/flutter-3.44.0
  koolbase release android --target-archs arm64,arm`,
	Args: cobra.ExactArgs(1),
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
	fmt.Printf("  Flutter SDK: %s (%s)\n\n", flutterBin, sdkSource)

	type abiArtifact struct {
		arch    string // CLI form: arm64 / arm
		abiDir  string // AAB lib dir: arm64-v8a / armeabi-v7a
		buildID string
		libDir  string
	}
	var arts []abiArtifact

	// 1. Build each ABI via the proven 'koolbase build android' path, then
	//    capture its stamped build_id and native libs.
	for _, arch := range releaseArchs {
		fmt.Printf("=== building ABI: %s ===\n", arch)
		// Clean between ABIs so no prior-ABI artifacts leak into the APK.
		clean := exec.Command(flutterBin, "clean")
		clean.Stdout, clean.Stderr = os.Stdout, os.Stderr
		_ = clean.Run()

		buildArgs := []string{"build", "android", "--engine", version, "--target-arch", arch, "--release"}
		if releaseFlutterSDK != "" {
			buildArgs = append(buildArgs, "--flutter-sdk", releaseFlutterSDK)
		}
		if releaseNoTreeShake {
			buildArgs = append(buildArgs, "--no-tree-shake-icons")
		}
		b := exec.Command(self, buildArgs...)
		b.Stdout, b.Stderr, b.Stdin = os.Stdout, os.Stderr, os.Stdin
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

		apk := filepath.Join(projectDir, "build", "app", "outputs", "flutter-apk", "app-release.apk")
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
	sh := exec.Command(flutterBin, shellArgs...)
	sh.Stdout, sh.Stderr, sh.Stdin = os.Stdout, os.Stderr, os.Stdin
	if err := sh.Run(); err != nil {
		return fmt.Errorf("app bundle shell build failed: %w", err)
	}
	shellAAB := filepath.Join(projectDir, "build", "app", "outputs", "bundle", "release", "app-release.aab")

	// 3. Assemble: swap Koolbase libs + write the per-ABI build_id map.
	outDir := filepath.Join(projectDir, "build", "app", "outputs", "koolbase")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	outAAB := filepath.Join(outDir, "app-release.aab")

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

	extraAbis, err := assembleReleaseAAB(shellAAB, outAAB, libSwaps, string(mapJSON), engined)
	if err != nil {
		return fmt.Errorf("assemble AAB: %w", err)
	}

	fmt.Printf("\n\u2713 Release bundle ready: %s\n", outAAB)
	for abi, bid := range buildIDMap {
		fmt.Printf("    %-13s build_id %s\n", abi, bid)
	}

	// Register a build_id release per ABI so the resolver can serve patches to
	// each ABI's devices. build_id mode: each ABI build_id is its own release;
	// app_version groups them. CreateRelease is idempotent server-side.
	cfg, cerr := config.Load()
	projectID := releaseProject
	if cerr == nil && projectID == "" {
		projectID = cfg.ProjectID
	}
	if cerr != nil || projectID == "" || cfg.APIKey == "" {
		fmt.Println("\nWARNING: releases NOT registered (no --project and no saved login).")
		fmt.Println("  Code Push cannot serve patches to these build_ids until registered.")
	} else {
		appVersion := readPubspecVersion(projectDir)
		apiClient := api.NewClient(cfg.BaseURL, cfg.APIKey)
		fmt.Println("\n=== registering releases ===")
		for _, a := range arts {
			rel, rerr := apiClient.CreateRelease(projectID, api.CreateReleaseRequest{
				BuildID:        a.buildID,
				FlutterVersion: baseFlutterVersion(version),
				Platform:       "android",
				AppVersion:     appVersion,
				MatchMode:      "build_id",
				Channel:        releaseChannel,
			})
			if rerr != nil {
				return fmt.Errorf("register release for %s (build_id %s): %w", a.abiDir, a.buildID, rerr)
			}
			fmt.Printf("  registered %s -> release %s (build_id %s, channel %s)\n", a.abiDir, rel.ID, a.buildID, releaseChannel)
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
func assembleReleaseAAB(shellPath, outPath string, libSwaps map[string]string, buildIDMapJSON string, engined map[string]bool) ([]string, error) {
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
	var out []string
	for a := range extra {
		out = append(out, a)
	}
	return out, nil
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
	releaseCmd.Flags().StringVar(&releaseChannel, "channel", "stable", "Release channel for the registered releases")
	rootCmd.AddCommand(releaseCmd)
}
