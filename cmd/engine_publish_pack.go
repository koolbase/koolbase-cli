package cmd

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// packLeanEngine copies the empirically-proven minimal engine file set from the
// build out-dir into stageDir/src/out/{<android-config>,host_release_arm64}/,
// where <android-config> is android_release_arm64 (arm64) or android_release_arm
// (arm). This mirrors pack_engine.sh: the raw out-dirs are ~20GB of build
// intermediates; only this subset is consumed by a --local-engine Android build
// (proven by a real build succeeding against exactly these files).
func packLeanEngine(engineSrcOut, stageDir, version, targetArch string) error {
	androidConfig, jarABI, ok := androidEngineConfig(targetArch)
	if !ok {
		return fmt.Errorf("unsupported target-arch %q — use 'arm64' or 'arm'", targetArch)
	}
	srcOut := filepath.Join(stageDir, "src", "out")
	androidDst := filepath.Join(srcOut, androidConfig)
	hostDst := filepath.Join(srcOut, "host_release_arm64")

	// Clean any prior staging for this version.
	if err := os.RemoveAll(stageDir); err != nil {
		return err
	}
	for _, d := range []string{androidDst, hostDst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	androidSrc := filepath.Join(engineSrcOut, androidConfig)
	hostSrc := filepath.Join(engineSrcOut, "host_release_arm64")

	// --- android target artifacts ---
	// whole dirs:
	for _, dir := range []string{"flutter_patched_sdk", "universal"} {
		if err := copyTree(filepath.Join(androidSrc, dir), filepath.Join(androidDst, dir)); err != nil {
			return fmt.Errorf("android %s: %w", dir, err)
		}
	}
	// named files (traced + known-essential); missing ones are skipped with a note.
	androidFiles := []string{
		"gen_snapshot",
		jarABI + "_release.jar", jarABI + "_release.maven-metadata.xml", jarABI + "_release.pom",
		"flutter_embedding_release.jar", "flutter_embedding_release.maven-metadata.xml", "flutter_embedding_release.pom",
		"libflutter.so", "icudtl.dat",
	}
	copyOptionalFiles(androidSrc, androidDst, androidFiles, "android")

	// --- host tools ---
	if err := copyTree(filepath.Join(hostSrc, "dart-sdk"), filepath.Join(hostDst, "dart-sdk")); err != nil {
		return fmt.Errorf("host dart-sdk: %w", err)
	}
	// gen/ selected pieces
	if err := os.MkdirAll(filepath.Join(hostDst, "gen"), 0o755); err != nil {
		return err
	}
	copyOptionalFiles(filepath.Join(hostSrc, "gen"), filepath.Join(hostDst, "gen"),
		[]string{"const_finder.dart.snapshot", "frontend_server_aot.dart.snapshot"}, "host gen")
	if err := copyTree(filepath.Join(hostSrc, "gen", "dart-pkg"), filepath.Join(hostDst, "gen", "dart-pkg")); err != nil {
		return fmt.Errorf("host gen/dart-pkg: %w", err)
	}
	if err := copyTree(filepath.Join(hostSrc, "shader_lib"), filepath.Join(hostDst, "shader_lib")); err != nil {
		return fmt.Errorf("host shader_lib: %w", err)
	}
	copyOptionalFiles(hostSrc, hostDst, []string{"font-subset", "impellerc"}, "host")

	return nil
}

func copyOptionalFiles(srcDir, dstDir string, names []string, label string) {
	for _, n := range names {
		src := filepath.Join(srcDir, n)
		if _, err := os.Stat(src); err != nil {
			fmt.Printf("    (%s: no %s — skipping)\n", label, n)
			continue
		}
		if err := copyFile(src, filepath.Join(dstDir, n)); err != nil {
			fmt.Printf("    (%s: %s copy failed: %v)\n", label, n, err)
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, _ := in.Stat()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err // caller decides if fatal
	}
	if !info.IsDir() {
		return copyFile(src, dst)
	}
	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
		} else if e.Type()&os.ModeSymlink != 0 {
			// resolve+copy symlink targets (dart-sdk has some); skip if dangling.
			if target, err := os.Readlink(s); err == nil {
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(s), target)
				}
				if _, err := os.Stat(target); err == nil {
					_ = copyFile(target, d)
				}
			}
		} else {
			if err := copyFile(s, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// zipDir zips parentDir/topEntry into zipPath, with topEntry as the single
// top-level directory (matching what `install` expects to unpack).
func zipDir(parentDir, topEntry, zipPath string) error {
	zf, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer zf.Close()
	zw := zip.NewWriter(zf)
	defer zw.Close()

	root := filepath.Join(parentDir, topEntry)
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(parentDir, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if rel != "." {
				_, err = zw.Create(strings.TrimSuffix(rel, "/") + "/")
			}
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = rel
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
}
