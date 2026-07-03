package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// appBinaryInfo is the result of analysing a built Flutter App binary for
// VM-level (System B) patching.
type appBinaryInfo struct {
	BuildID     []byte // SHA-256(instructions)[0:8]
	InstrSize   uint64 // base instructions length (bytes the engine hashes)
	DataSize    uint64 // base isolate-data snapshot length
	PriceOffset int64  // file offset of the 3 price digits, or -1 if absent
}

const (
	kbInstrSym    = "_kDartIsolateSnapshotInstructions"
	kbDataSym     = "_kDartIsolateSnapshotData"
	kbPriceMarker = "KBPRICE@@@100@@@END"
)

// readMagic returns the first n bytes of a file for format detection.
func readMagic(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, n)
	got, _ := f.Read(head)
	return head[:got], nil
}

// analyzeAppBinary inspects a built Flutter App binary (Mach-O on macOS or ELF
// libapp.so on Android, auto-detected) and returns the data needed to mint a
// .kbpatch for it.
func analyzeAppBinary(appPath string) (*appBinaryInfo, error) {
	head, err := readMagic(appPath, 8)
	if err != nil {
		return nil, fmt.Errorf("read binary head: %w", err)
	}
	switch {
	case isELF(head):
		return analyzeELF(appPath)
	case isMachO(head):
		return analyzeMachO(appPath)
	default:
		return nil, fmt.Errorf("unrecognized binary format (not Mach-O or ELF): %s", appPath)
	}
}

// downloadToTemp streams a URL (a presigned R2 GET) to a temp file and returns
// its path. Caller is responsible for removing it.
func downloadToTemp(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download failed: status %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(os.TempDir(), "kb_base_*.so")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// extractSnapshotBlobs returns the raw isolate data + instructions blobs from a
// built App binary (Mach-O or ELF) — the payload of a kind=3 whole-blob
// replacement patch.
func extractSnapshotBlobs(appPath string) (data []byte, instr []byte, err error) {
	head, herr := readMagic(appPath, 8)
	if herr != nil {
		return nil, nil, fmt.Errorf("read binary head: %w", herr)
	}
	switch {
	case isELF(head):
		return extractSnapshotBlobsELF(appPath)
	case isMachO(head):
		return extractSnapshotBlobsMachO(appPath)
	default:
		return nil, nil, fmt.Errorf("unrecognized binary format (not Mach-O or ELF): %s", appPath)
	}
}

// openAppMacho opens a built App binary as a thin arm64 Mach-O, handling the
// universal/fat wrapper Flutter produces. Caller must call the returned closer.
func openAppMacho(appPath string) (*macho.File, func() error, error) {
	if ff, ferr := macho.OpenFat(appPath); ferr == nil {
		var mf *macho.File
		for i := range ff.Arches {
			if ff.Arches[i].Cpu == macho.CpuArm64 {
				mf = ff.Arches[i].File
				break
			}
		}
		if mf == nil {
			ff.Close()
			return nil, nil, fmt.Errorf("no arm64 slice in fat binary")
		}
		return mf, ff.Close, nil
	}
	m, merr := macho.Open(appPath)
	if merr != nil {
		return nil, nil, fmt.Errorf("failed to parse Mach-O: %w", merr)
	}
	return m, m.Close, nil
}

// symbolSectionSlice returns the bytes from the named symbol to the end of the
// section that contains it — the exact range the engine recomputes for build_id
// (instructions) and copies for reconstruction (data/instructions).
func symbolSectionSlice(mf *macho.File, symName string) ([]byte, error) {
	var symVA uint64
	found := false
	if mf.Symtab != nil {
		for _, s := range mf.Symtab.Syms {
			if s.Name == symName {
				symVA = s.Value
				found = true
				break
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("symbol %s not found", symName)
	}

	var sec *macho.Section
	for _, s := range mf.Sections {
		if symVA >= s.Addr && symVA < s.Addr+s.Size {
			sec = s
			break
		}
	}
	if sec == nil {
		return nil, fmt.Errorf("no section contains 0x%x for %s", symVA, symName)
	}

	secData, derr := sec.Data()
	if derr != nil {
		return nil, fmt.Errorf("failed to read section data for %s: %w", symName, derr)
	}
	startInSec := symVA - sec.Addr
	if startInSec > uint64(len(secData)) {
		return nil, fmt.Errorf("symbol offset 0x%x beyond section data 0x%x for %s",
			startInSec, len(secData), symName)
	}
	return secData[startInSec:], nil
}

// analyzeMachO inspects a built Flutter App Mach-O and returns the data needed
// to mint a .kbpatch for it (marker, identity, or whole-blob).
func analyzeMachO(appPath string) (*appBinaryInfo, error) {
	raw, err := os.ReadFile(appPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read binary: %w", err)
	}

	// Locate the price marker if present (marker patches only); non-fatal.
	marker := []byte(kbPriceMarker)
	priceOffset := int64(-1)
	for i := 0; i+len(marker) <= len(raw); i++ {
		if bytes.Equal(raw[i:i+len(marker)], marker) {
			priceOffset = int64(i + len("KBPRICE@@@"))
			break
		}
	}

	mf, closeFn, err := openAppMacho(appPath)
	if err != nil {
		return nil, err
	}
	defer closeFn()

	instrBytes, err := symbolSectionSlice(mf, kbInstrSym)
	if err != nil {
		return nil, err
	}
	dataBytes, err := symbolSectionSlice(mf, kbDataSym)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(instrBytes)

	return &appBinaryInfo{
		BuildID:     sum[:8],
		InstrSize:   uint64(len(instrBytes)),
		DataSize:    uint64(len(dataBytes)),
		PriceOffset: priceOffset,
	}, nil
}

// extractSnapshotBlobsMachO returns the raw isolate data + instructions blobs
// from a built Mach-O App binary.
func extractSnapshotBlobsMachO(appPath string) (data []byte, instr []byte, err error) {
	mf, closeFn, oerr := openAppMacho(appPath)
	if oerr != nil {
		return nil, nil, oerr
	}
	defer closeFn()
	data, err = symbolSectionSlice(mf, kbDataSym)
	if err != nil {
		return nil, nil, err
	}
	instr, err = symbolSectionSlice(mf, kbInstrSym)
	if err != nil {
		return nil, nil, err
	}
	return data, instr, nil
}

// buildKBPMPatch mints the signed 128-byte marker (kind=0) blob.
func buildKBPMPatch(info *appBinaryInfo, newPrice, privateKeyPath string) ([]byte, error) {
	if len(newPrice) != 3 {
		return nil, fmt.Errorf("new price must be exactly 3 digits (e.g. 080), got %q", newPrice)
	}
	if info.PriceOffset < 0 {
		return nil, fmt.Errorf("price marker %q not found in binary", kbPriceMarker)
	}
	keyBytes, err := loadKey(privateKeyPath)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 128)
	copy(buf[0:4], []byte("KBPM"))
	binary.LittleEndian.PutUint16(buf[4:6], 1)
	binary.LittleEndian.PutUint16(buf[6:8], 64)
	// buf[8] (kind) = 0 = marker
	copy(buf[16:24], info.BuildID)
	binary.LittleEndian.PutUint64(buf[24:32], uint64(info.PriceOffset))
	buf[40] = newPrice[0]
	buf[41] = newPrice[1]
	buf[42] = newPrice[2]
	binary.LittleEndian.PutUint64(buf[56:64], info.InstrSize)

	sig := ed25519.Sign(ed25519.PrivateKey(keyBytes), buf[0:64])
	copy(buf[64:128], sig)
	return buf, nil
}

// buildWholeBlobPatch mints the signed 128-byte whole-blob IDENTITY (kind=2)
// header. No payload: the engine reconstructs by copying the running base.
func buildWholeBlobPatch(info *appBinaryInfo, privateKeyPath string) ([]byte, error) {
	keyBytes, err := loadKey(privateKeyPath)
	if err != nil {
		return nil, err
	}
	if info.InstrSize == 0 || info.DataSize == 0 {
		return nil, fmt.Errorf("instr_size=%d data_size=%d — analysis incomplete", info.InstrSize, info.DataSize)
	}

	buf := make([]byte, 128)
	copy(buf[0:4], []byte("KBPM"))
	binary.LittleEndian.PutUint16(buf[4:6], 1)
	binary.LittleEndian.PutUint16(buf[6:8], 64)
	buf[8] = 2 // kind = identity
	copy(buf[16:24], info.BuildID)
	binary.LittleEndian.PutUint64(buf[24:32], info.DataSize)
	binary.LittleEndian.PutUint64(buf[56:64], info.InstrSize)

	sig := ed25519.Sign(ed25519.PrivateKey(keyBytes), buf[0:64])
	copy(buf[64:128], sig)
	return buf, nil
}

// buildWholeBlobReplacePatch mints a signed kind=3 patch: a 128-byte header plus
// a payload carrying a NEW snapshot (newData || newInstr) extracted from a
// recompiled build. build_id pins it to the running BASE binary; the payload is
// bound to the signed header via SHA-256(payload)[0:16].
//
//	[8]       kind = 3
//	[16..23]  build_id (base)
//	[24..31]  len(newData)        [32..39] len(newInstr)
//	[40..55]  SHA-256(payload)[0:16]
//	[56..63]  base instr_size (for the build_id check)
//	[64..127] Ed25519 sig over [0..63]
//	[128..]   newData || newInstr
func buildWholeBlobReplacePatch(base *appBinaryInfo, newData, newInstr []byte, privateKeyPath string) ([]byte, error) {
	keyBytes, err := loadKey(privateKeyPath)
	if err != nil {
		return nil, err
	}
	if len(newData) == 0 || len(newInstr) == 0 {
		return nil, fmt.Errorf("empty new blobs: data=%d instr=%d", len(newData), len(newInstr))
	}
	if base.InstrSize == 0 {
		return nil, fmt.Errorf("base instr_size is 0 — base analysis incomplete")
	}

	payload := make([]byte, 0, len(newData)+len(newInstr))
	payload = append(payload, newData...)
	payload = append(payload, newInstr...)
	ph := sha256.Sum256(payload)

	header := make([]byte, 128)
	copy(header[0:4], []byte("KBPM"))
	binary.LittleEndian.PutUint16(header[4:6], 1)
	binary.LittleEndian.PutUint16(header[6:8], 64)
	header[8] = 3 // kind = full replacement
	copy(header[16:24], base.BuildID)
	binary.LittleEndian.PutUint64(header[24:32], uint64(len(newData)))
	binary.LittleEndian.PutUint64(header[32:40], uint64(len(newInstr)))
	copy(header[40:56], ph[:16])
	binary.LittleEndian.PutUint64(header[56:64], base.InstrSize)

	sig := ed25519.Sign(ed25519.PrivateKey(keyBytes), header[0:64])
	copy(header[64:128], sig)

	out := make([]byte, 0, 128+len(payload))
	out = append(out, header...)
	out = append(out, payload...)
	return out, nil
}

func loadKey(privateKeyPath string) ([]byte, error) {
	keyBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("private key size wrong: got %d want %d", len(keyBytes), ed25519.PrivateKeySize)
	}
	return keyBytes, nil
}

// stampBuildId writes the build_id hex into the app bundle so the SDK can report
// it at runtime, at <X.app>/Contents/Resources/koolbase_build_id. Run before
// code-signing. macOS-only for now.
func stampBuildId(binaryPath, buildID string) (string, error) {
	appRoot, err := appBundleRoot(binaryPath)
	if err != nil {
		return "", err
	}
	resDir := filepath.Join(appRoot, "Contents", "Resources")
	if _, err := os.Stat(resDir); err != nil {
		return "", fmt.Errorf("Resources dir not found at %s: %w", resDir, err)
	}
	out := filepath.Join(resDir, "koolbase_build_id")
	if err := os.WriteFile(out, []byte(buildID), 0o644); err != nil {
		return "", err
	}
	return out, nil
}

// appBundleRoot walks up from the App binary to the enclosing *.app directory.
func appBundleRoot(binaryPath string) (string, error) {
	p := filepath.Clean(binaryPath)
	for p != "/" && p != "." {
		if strings.HasSuffix(p, ".app") {
			return p, nil
		}
		p = filepath.Dir(p)
	}
	return "", fmt.Errorf("no enclosing .app bundle found for %s", binaryPath)
}
// buildKBPIPatch mints a signed kind=5 patch: a 128-byte KBPM header plus the
// KBPI container as payload. Mirrors buildWholeBlobReplacePatch (kind=3): the
// payload is bound to the signed header via SHA-256(payload)[0:16], and build_id
// pins the patch to the running BASE binary.
//
//	[8]       kind = 5 (iOS bytecode / KBPI)
//	[16..23]  build_id (base)
//	[24..31]  len(KBPI payload)
//	[40..55]  SHA-256(payload)[0:16]
//	[56..63]  base instr_size (for the build_id check)
//	[64..127] Ed25519 sig over [0..63]
//	[128..]   KBPI container
func buildKBPIPatch(base *appBinaryInfo, kbpi []byte, privateKeyPath string) ([]byte, error) {
	keyBytes, err := loadKey(privateKeyPath)
	if err != nil {
		return nil, err
	}
	if len(kbpi) == 0 {
		return nil, fmt.Errorf("empty KBPI payload")
	}
	if base.InstrSize == 0 {
		return nil, fmt.Errorf("base instr_size is 0 — base analysis incomplete")
	}

	ph := sha256.Sum256(kbpi)

	header := make([]byte, 128)
	copy(header[0:4], []byte("KBPM"))
	binary.LittleEndian.PutUint16(header[4:6], 1)
	binary.LittleEndian.PutUint16(header[6:8], 64)
	header[8] = 5 // kind = iOS bytecode / KBPI
	copy(header[16:24], base.BuildID)
	binary.LittleEndian.PutUint64(header[24:32], uint64(len(kbpi)))
	copy(header[40:56], ph[:16])
	binary.LittleEndian.PutUint64(header[56:64], base.InstrSize)

	sig := ed25519.Sign(ed25519.PrivateKey(keyBytes), header[0:64])
	copy(header[64:128], sig)

	out := make([]byte, 0, 128+len(kbpi))
	out = append(out, header...)
	out = append(out, kbpi...)
	return out, nil
}
