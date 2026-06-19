package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

// --- flags ---
var (
	pubFlutterVersion string
	pubEngineSrc      string
	pubRevision       int
	pubKeyPath        string
	pubKeepZip        bool
	pubTargetArch     string
)

// enginePublishCmd packs, signs, uploads, registers, and publishes a Koolbase
// engine artifact. Operator-only: authenticates to /internal/engines/* with
// the internal key from $KOOLBASE_INTERNAL_KEY. Replaces the pack/publish shell
// scripts with a single, robust command.
var enginePublishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Pack, sign, upload, and publish a Koolbase engine (operator-only)",
	Long: `Publish a built Koolbase engine to the registry.

Packs the lean engine artifact from the build out-dir, zips it, signs its
SHA-256 digest with the engine signing key, uploads it to R2 via a presigned
URL, registers it, and publishes it.

Requires the internal API key in the environment:
  export KOOLBASE_INTERNAL_KEY=$(doppler secrets get INTERNAL_API_KEY --plain --config prd)

Example:
  koolbase engine publish --flutter-version 3.44.0 \
    --engine-src ~/Codes/koolbase-engine-344/engine/src/out`,
	RunE: runEnginePublish,
}

const (
	pubHostArch       = "arm64" // host build arch (Apple silicon)
	pubTargetPlatform = "android"
)

func pubHostPlatform() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return runtime.GOOS
}

func runEnginePublish(cmd *cobra.Command, args []string) error {
	internalKey := os.Getenv("KOOLBASE_INTERNAL_KEY")
	if internalKey == "" {
		return fmt.Errorf("KOOLBASE_INTERNAL_KEY is not set\n" +
			"  export KOOLBASE_INTERNAL_KEY=$(doppler secrets get INTERNAL_API_KEY --plain --config prd)")
	}
	if pubFlutterVersion == "" {
		return fmt.Errorf("--flutter-version is required (e.g. 3.44.0)")
	}
	home, _ := os.UserHomeDir()
	if pubKeyPath == "" {
		pubKeyPath = filepath.Join(home, "Codes", "vm-study", "engine-keys", "engine_private.key")
	}
	if pubEngineSrc == "" {
		return fmt.Errorf("--engine-src is required (path to engine/src/out)")
	}

	version := fmt.Sprintf("%s-koolbase.%d", pubFlutterVersion, pubRevision)
	fmt.Printf("Publishing engine %s\n", version)
	fmt.Printf("  host:   %s/%s\n  target: %s/%s\n\n", pubHostPlatform(), pubHostArch, pubTargetPlatform, pubTargetArch)

	// 1. Pack lean artifact.
	stageDir := filepath.Join(home, ".koolbase-pack", version)
	fmt.Println("==> 1/6 Packing lean engine…")
	if err := packLeanEngine(pubEngineSrc, stageDir, version, pubTargetArch); err != nil {
		return fmt.Errorf("pack: %w", err)
	}

	// 2. Zip.
	zipPath := filepath.Join(os.TempDir(), "koolbase-engine-"+version+".zip")
	fmt.Println("==> 2/6 Zipping…")
	if err := zipDir(filepath.Dir(stageDir), version, zipPath); err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	if !pubKeepZip {
		defer os.Remove(zipPath)
	}
	size, sha, err := sizeAndSHA(zipPath)
	if err != nil {
		return err
	}
	fmt.Printf("    zip: %s (%d MB)  sha256 %s…\n", zipPath, size/(1024*1024), sha[:16])

	// 3. Sign.
	fmt.Println("==> 3/6 Signing digest (Ed25519)…")
	sig, err := signDigest(pubKeyPath, sha)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	fmt.Printf("    signature: %s… (%d hex)\n", sig[:32], len(sig))

	// 4-6 handled by the API helper.
	op := &internalAPI{base: engineAPIBase(), key: internalKey}
	axes := engineAxes{
		FlutterVersion: pubFlutterVersion, Revision: pubRevision,
		HostPlatform: pubHostPlatform(), HostArch: pubHostArch,
		TargetPlatform: pubTargetPlatform, TargetArch: pubTargetArch,
		SizeBytes: size, SHA256: sha, Signature: sig,
	}

	fmt.Println("==> 4/6 Requesting presigned upload URL…")
	uploadURL, r2Key, err := op.getUploadURL(axes)
	if err != nil {
		return err
	}
	fmt.Printf("    r2_key: %s\n", r2Key)

	fmt.Println("==> 5/6 Uploading to R2…")
	if err := op.putFile(uploadURL, zipPath); err != nil {
		return err
	}

	fmt.Println("==> 6/6 Registering + publishing…")
	axes.R2Key = r2Key
	id, err := op.createEngine(axes)
	if err != nil {
		return err
	}
	if err := op.publishEngine(id); err != nil {
		return err
	}

	fmt.Printf("\n✓ Published engine %s (id %s)\n", version, id)
	fmt.Println("  Verify: koolbase engine list")
	return nil
}

// engineAPIBase returns the API base for operator calls. Honors KOOLBASE_API.
func engineAPIBase() string {
	if v := os.Getenv("KOOLBASE_API"); v != "" {
		return v
	}
	return "https://api.koolbase.com"
}

// --- local helpers: sign, hash, zip (pack/zip defined in engine_publish_pack.go) ---

func signDigest(keyPath, shaHex string) (string, error) {
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return "", fmt.Errorf("read signing key: %w", err)
	}
	if len(keyBytes) != 64 {
		return "", fmt.Errorf("bad signing key length %d (want 64)", len(keyBytes))
	}
	digest, err := hex.DecodeString(shaHex)
	if err != nil || len(digest) != 32 {
		return "", fmt.Errorf("bad sha256")
	}
	sig := ed25519.Sign(ed25519.PrivateKey(keyBytes), digest)
	return hex.EncodeToString(sig), nil
}

func sizeAndSHA(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	st, _ := f.Stat()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return 0, "", err
	}
	return st.Size(), hex.EncodeToString(h.Sum(nil)), nil
}

// --- internal API client (X-Internal-Key auth, long timeout for uploads) ---

type engineAxes struct {
	FlutterVersion string
	Revision       int
	HostPlatform   string
	HostArch       string
	TargetPlatform string
	TargetArch     string
	R2Key          string
	SizeBytes      int64
	SHA256         string
	Signature      string
}

func (a engineAxes) createBody() map[string]interface{} {
	return map[string]interface{}{
		"flutter_version":   a.FlutterVersion,
		"koolbase_revision": a.Revision,
		"host_platform":     a.HostPlatform,
		"host_arch":         a.HostArch,
		"target_platform":   a.TargetPlatform,
		"target_arch":       a.TargetArch,
		"r2_key":            a.R2Key,
		"size_bytes":        a.SizeBytes,
		"sha256":            a.SHA256,
		"signature":         a.Signature,
	}
}

type internalAPI struct {
	base string
	key  string
}

func (o *internalAPI) client() *http.Client {
	return &http.Client{Timeout: 30 * time.Minute} // large for ~500MB uploads
}

func (o *internalAPI) post(path string, body interface{}) ([]byte, int, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, o.base+path, bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", o.key)
	return o.send(req)
}

func (o *internalAPI) patch(path string, body interface{}) ([]byte, int, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPatch, o.base+path, bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", o.key)
	return o.send(req)
}

func (o *internalAPI) send(req *http.Request) ([]byte, int, error) {
	resp, err := o.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
}

func (o *internalAPI) getUploadURL(a engineAxes) (url, r2Key string, err error) {
	body := a.createBody()
	body["r2_key"] = "placeholder"
	data, status, err := o.post("/internal/engines/upload-url", body)
	if err != nil {
		return "", "", err
	}
	if status != http.StatusOK {
		return "", "", fmt.Errorf("upload-url failed (%d): %s", status, string(data))
	}
	var out struct {
		URL   string `json:"url"`
		R2Key string `json:"r2_key"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", "", err
	}
	return out.URL, out.R2Key, nil
}

func (o *internalAPI) putFile(signedURL, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, _ := f.Stat()
	req, err := http.NewRequest(http.MethodPut, signedURL, f)
	if err != nil {
		return err
	}
	req.ContentLength = st.Size()
	req.Header.Set("Content-Type", "application/zip")
	resp, err := o.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("R2 upload failed (%d): %s", resp.StatusCode, string(b))
	}
	return nil
}

func (o *internalAPI) createEngine(a engineAxes) (string, error) {
	data, status, err := o.post("/internal/engines", a.createBody())
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return "", fmt.Errorf("create failed (%d): %s", status, string(data))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (o *internalAPI) publishEngine(id string) error {
	data, status, err := o.patch("/internal/engines/"+id, map[string]string{"status": "published"})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("publish failed (%d): %s", status, string(data))
	}
	return nil
}

func init() {
	enginePublishCmd.Flags().StringVar(&pubFlutterVersion, "flutter-version", "", "Flutter version (e.g. 3.44.0)")
	enginePublishCmd.Flags().StringVar(&pubEngineSrc, "engine-src", "", "Path to engine build out-dir (engine/src/out)")
	enginePublishCmd.Flags().IntVar(&pubRevision, "revision", 1, "Koolbase engine revision")
	enginePublishCmd.Flags().StringVar(&pubKeyPath, "key", "", "Ed25519 engine signing key path")
	enginePublishCmd.Flags().BoolVar(&pubKeepZip, "keep-zip", false, "Keep the built zip after publishing")
	enginePublishCmd.Flags().StringVar(&pubTargetArch, "target-arch", "arm64", "Target ABI: arm64 (arm64-v8a, default) or arm (armeabi-v7a)")
	engineCmd.AddCommand(enginePublishCmd)
}
