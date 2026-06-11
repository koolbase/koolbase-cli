// Package engine manages locally-installed Koolbase Flutter engines under
// ~/.koolbase/engines/. It downloads engine archives from a signed URL,
// verifies their SHA-256, and unpacks them to a versioned directory the
// `koolbase build` command points Flutter at via --local-engine.
package engine

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InstallDir returns ~/.koolbase/engines, creating nothing. Callers that
// write must MkdirAll first.
func InstallDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".koolbase", "engines"), nil
}

// VersionDir returns the install path for a specific engine version string
// (e.g. "3.22.3-koolbase.1"). This is the directory passed to Flutter.
func VersionDir(version string) (string, error) {
	base, err := InstallDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, version), nil
}

// IsInstalled reports whether a version directory exists and is non-empty.
func IsInstalled(version string) (bool, error) {
	dir, err := VersionDir(version)
	if err != nil {
		return false, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return len(entries) > 0, nil
}

// ListInstalled returns the version strings of all locally-installed engines.
func ListInstalled() ([]string, error) {
	base, err := InstallDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// Remove deletes an installed engine version directory.
func Remove(version string) error {
	dir, err := VersionDir(version)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("engine %s is not installed", version)
	}
	return os.RemoveAll(dir)
}

// ProgressFunc is called periodically during download with bytes downloaded
// and total bytes (total may be 0 if unknown).
type ProgressFunc func(downloaded, total int64)

// Install downloads the engine zip from signedURL, verifies its SHA-256
// against wantSHA, and extracts it into ~/.koolbase/engines/{version}/.
// If the version is already installed it returns nil without re-downloading.
func Install(version, signedURL, wantSHA string, totalBytes int64, progress ProgressFunc) error {
	installed, err := IsInstalled(version)
	if err != nil {
		return err
	}
	if installed {
		return nil
	}

	base, err := InstallDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}

	// Download to a temp file in the install dir (same filesystem, atomic rename).
	tmpFile, err := os.CreateTemp(base, "engine-*.zip.part")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) // no-op if we renamed/cleaned it already

	resp, err := http.Get(signedURL)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmpFile.Close()
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// Stream to disk while hashing, so we never hold 1.3GB in memory.
	hasher := sha256.New()
	pw := &progressWriter{total: totalBytes, fn: progress, last: time.Now()}
	mw := io.MultiWriter(tmpFile, hasher, pw)

	if _, err := io.Copy(mw, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write download: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	gotSHA := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(gotSHA, wantSHA) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", wantSHA, gotSHA)
	}

	// Extract into a temp dir, then rename into place atomically.
	stageDir, err := os.MkdirTemp(base, version+".staging-*")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	defer os.RemoveAll(stageDir)

	if err := unzip(tmpPath, stageDir); err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	// The zip contains a single top-level dir (the artifact name). Find it and
	// promote its contents so VersionDir points directly at the engine files.
	finalDir, err := VersionDir(version)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(finalDir); err != nil {
		return fmt.Errorf("clear final dir: %w", err)
	}

	inner, err := singleTopLevelDir(stageDir)
	if err != nil {
		return err
	}
	src := stageDir
	if inner != "" {
		src = filepath.Join(stageDir, inner)
	}
	if err := os.Rename(src, finalDir); err != nil {
		return fmt.Errorf("install rename: %w", err)
	}

	return nil
}

// singleTopLevelDir returns the name of the sole directory at the root of
// dir, or "" if there are multiple entries / files at the root.
func singleTopLevelDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		return entries[0].Name(), nil
	}
	return "", nil
}

// unzip extracts a zip archive to dest, guarding against path-traversal.
func unzip(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}

	for _, f := range r.File {
		target := filepath.Join(dest, f.Name)
		// Zip-slip guard: ensure target stays within dest.
		targetAbs, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(targetAbs, destAbs+string(os.PathSeparator)) && targetAbs != destAbs {
			return fmt.Errorf("illegal path in archive: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc) //nolint:gosec // sizes bounded by our own engine artifacts
	return err
}

// progressWriter counts bytes and throttles progress callbacks to ~4/sec.
type progressWriter struct {
	downloaded int64
	total      int64
	fn         ProgressFunc
	last       time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n := len(b)
	p.downloaded += int64(n)
	if p.fn != nil && time.Since(p.last) > 250*time.Millisecond {
		p.fn(p.downloaded, p.total)
		p.last = time.Now()
	}
	return n, nil
}
