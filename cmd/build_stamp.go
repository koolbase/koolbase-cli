package cmd

import (
	"archive/zip"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// androidABIDir maps a --target-arch value to its Android ABI directory name
// inside the APK (lib/<abiDir>/). Defaults to arm64-v8a.
func androidABIDir(targetArch string) string {
	switch targetArch {
	case "arm", "armeabi-v7a":
		return "armeabi-v7a"
	default: // "arm64", "arm64-v8a", ""
		return "arm64-v8a"
	}
}

// androidEngineConfig maps a --target-arch value to the Flutter engine build
// config dir (out/<config>) and the per-ABI artifact prefix used by the engine
// JAR/POM names. ok is false for unrecognized values. Single source of truth
// shared by `koolbase build` and `koolbase engine publish`.
func androidEngineConfig(targetArch string) (config, abiPrefix string, ok bool) {
	switch targetArch {
	case "arm64", "arm64-v8a", "":
		return "android_release_arm64", "arm64_v8a", true
	case "arm", "armeabi-v7a":
		return "android_release", "armeabi_v7a", true
	default:
		return "", "", false
	}
}

// stampBuildIDIntoAssets is the invisible Android stamping step run by
// `koolbase build android`. It computes the build_id from the freshly built
// libapp.so and writes it into assets/koolbase_build_id so the SDK can report
// it at runtime — closing the loop without any manual developer step.
//
// Returns (buildID, changed, error). `changed` is true if the asset content was
// updated (caller rebuilds once so the asset is bundled); false means the asset
// already held this build_id (idempotent — no rebuild needed). Asset-only
// changes do NOT alter the build_id (verified on-device), so the rebuild is safe.
func stampBuildIDIntoAssets(projectDir, apkPath, abiDir string) (buildID string, changed bool, err error) {
	libapp, cleanup, err := extractLibappFromAPK(apkPath, abiDir)
	if err != nil {
		return "", false, fmt.Errorf("locate libapp.so: %w", err)
	}
	defer cleanup()

	info, err := analyzeAppBinary(libapp)
	if err != nil {
		return "", false, fmt.Errorf("analyze libapp.so: %w", err)
	}
	buildID = hex.EncodeToString(info.BuildID)

	assetsDir := filepath.Join(projectDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		return "", false, fmt.Errorf("create assets dir: %w", err)
	}
	assetPath := filepath.Join(assetsDir, "koolbase_build_id")

	// Idempotency: only rewrite + flag changed if content differs.
	if existing, rerr := os.ReadFile(assetPath); rerr == nil &&
		strings.TrimSpace(string(existing)) == buildID {
		changed = false
	} else {
		if werr := os.WriteFile(assetPath, []byte(buildID+"\n"), 0o644); werr != nil {
			return "", false, fmt.Errorf("write build_id asset: %w", werr)
		}
		changed = true
	}

	if derr := ensurePubspecAsset(projectDir, "koolbase_build_id"); derr != nil {
		return "", false, derr
	}
	return buildID, changed, nil
}

// stampFlutterVersionIntoAssets writes the bare Flutter version (engine suffix
// stripped, e.g. "3.32.8" from "3.32.8-koolbase.1") into
// assets/koolbase_flutter_version so the SDK can report it on patch-check and the
// resolver can refuse a patch built on a different engine. Unlike build_id this
// value is known up front (from --engine) and is identical across ABIs, so it is
// a bare string written directly — no binary analysis, no per-ABI map.
//
// Returns (changed, error). `changed` is true if the asset content was updated
// (caller rebuilds once so it is bundled); false means it already held this value.
func stampFlutterVersionIntoAssets(projectDir, engineVersion string) (changed bool, err error) {
	flutterVersion := baseFlutterVersion(engineVersion)
	assetsDir := filepath.Join(projectDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		return false, fmt.Errorf("create assets dir: %w", err)
	}
	assetPath := filepath.Join(assetsDir, "koolbase_flutter_version")
	if existing, rerr := os.ReadFile(assetPath); rerr == nil &&
		strings.TrimSpace(string(existing)) == flutterVersion {
		changed = false
	} else {
		if werr := os.WriteFile(assetPath, []byte(flutterVersion+"\n"), 0o644); werr != nil {
			return false, fmt.Errorf("write flutter_version asset: %w", werr)
		}
		changed = true
	}
	if derr := ensurePubspecAsset(projectDir, "koolbase_flutter_version"); derr != nil {
		return false, derr
	}
	return changed, nil
}

// extractLibappFromAPK pulls lib/<abiDir>/libapp.so out of the APK into a temp
// file and returns its path plus a cleanup func. abiDir is the Android ABI
// directory name, e.g. "arm64-v8a" or "armeabi-v7a".
func extractLibappFromAPK(apkPath, abiDir string) (string, func(), error) {
	r, err := zip.OpenReader(apkPath)
	if err != nil {
		return "", func() {}, err
	}
	defer r.Close()

	want := "lib/" + abiDir + "/libapp.so"
	var zf *zip.File
	for _, f := range r.File {
		if f.Name == want {
			zf = f
			break
		}
	}
	if zf == nil {
		return "", func() {}, fmt.Errorf("%s not found in APK", want)
	}

	tmp, err := os.CreateTemp("", "koolbase-libapp-*.so")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.Remove(tmp.Name()) }

	rc, err := zf.Open()
	if err != nil {
		tmp.Close()
		cleanup()
		return "", func() {}, err
	}
	defer rc.Close()
	if _, err := io.Copy(tmp, rc); err != nil { //nolint:gosec // our own build artifact
		tmp.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return tmp.Name(), cleanup, nil
}

// ensurePubspecAsset makes sure pubspec.yaml declares assets/<assetName>
// under the flutter: section so the file is bundled. Idempotent.
func ensurePubspecAsset(projectDir, assetName string) error {
	p := filepath.Join(projectDir, "pubspec.yaml")
	raw, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("read pubspec.yaml: %w", err)
	}
	s := string(raw)
	if strings.Contains(s, "assets/"+assetName) {
		return nil // already declared
	}

	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines)+2)
	inFlutter := false
	flutterIndent := ""
	insertedUnderExistingAssets := false
	addedBlock := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Detect the top-level `flutter:` key (no leading whitespace).
		if !inFlutter && (line == "flutter:" || strings.HasPrefix(line, "flutter:")) &&
			!strings.HasPrefix(line, " ") {
			inFlutter = true
			flutterIndent = "  "
			out = append(out, line)
			continue
		}

		// If inside flutter: and we find an existing `assets:` key, inject our
		// entry right after it.
		if inFlutter && !addedBlock && strings.HasPrefix(trimmed, "assets:") &&
			strings.HasPrefix(line, flutterIndent) {
			out = append(out, line)
			out = append(out, flutterIndent+"  - assets/"+assetName)
			insertedUnderExistingAssets = true
			addedBlock = true
			continue
		}

		// Leaving the flutter: block (a new top-level key) without having found
		// an assets: list — add a fresh assets: block before exiting.
		if inFlutter && !addedBlock && trimmed != "" && !strings.HasPrefix(line, " ") &&
			!strings.HasPrefix(line, "flutter:") {
			out = append(out, flutterIndent+"assets:")
			out = append(out, flutterIndent+"  - assets/"+assetName)
			addedBlock = true
			inFlutter = false
			out = append(out, line)
			continue
		}

		out = append(out, line)
	}

	// flutter: was the last block and had no assets: — append one.
	if inFlutter && !addedBlock {
		out = append(out, flutterIndent+"assets:")
		out = append(out, flutterIndent+"  - assets/"+assetName)
		addedBlock = true
	}

	_ = insertedUnderExistingAssets
	if !addedBlock {
		return fmt.Errorf("could not find a flutter: section in pubspec.yaml to add assets")
	}

	return os.WriteFile(p, []byte(strings.Join(out, "\n")), 0o644)
}
