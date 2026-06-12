package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// appBinaryInfo is the result of analysing a built Flutter App binary for
// VM-level (System B) patching:
//   - BuildID: the id the engine verifies against (SHA-256(instructions)[0:8])
//   - InstrSize: bytes the engine hashes / the base instructions length
//   - DataSize: the base isolate-data snapshot length (needed to reconstruct
//     the whole-blob snapshot on device)
//   - PriceOffset: file offset of the marker's 3 price digits, or -1 if the
//     binary has no price marker (whole-blob patches don't use it)
//
// Faithful port of the verified writer_macho_v2 tool — same loader
// (OpenFat → arm64 slice), same symbols, same section-based slice (from the
// symbol to the end of its section) — so the build_id and sizes are
// byte-for-byte what the patched engine recomputes at apply time. Do NOT
// "optimise" the slice range.
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

// symbolSectionSlice returns the bytes from the named symbol to the end of the
// section that contains it — the exact range the engine recomputes for build_id
// (instructions) and copies for reconstruction (data).
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

// analyzeAppBinary inspects a built Flutter App Mach-O and returns the data
// needed to mint a .kbpatch for it (marker or whole-blob).
func analyzeAppBinary(appPath string) (*appBinaryInfo, error) {
	raw, err := os.ReadFile(appPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read binary: %w", err)
	}

	// Locate the price marker if present; the 3 digits sit right after
	// "KBPRICE@@@". Absence is NOT an error — whole-blob patches don't use it.
	marker := []byte(kbPriceMarker)
	priceOffset := int64(-1)
	for i := 0; i+len(marker) <= len(raw); i++ {
		if bytes.Equal(raw[i:i+len(marker)], marker) {
			priceOffset = int64(i + len("KBPRICE@@@"))
			break
		}
	}

	// The App binary is a universal/fat Mach-O (even with one slice); use
	// OpenFat and pick arm64, falling back to thin Mach-O.
	var mf *macho.File
	var closeFn func() error
	if ff, ferr := macho.OpenFat(appPath); ferr == nil {
		for i := range ff.Arches {
			if ff.Arches[i].Cpu == macho.CpuArm64 {
				mf = ff.Arches[i].File
				break
			}
		}
		closeFn = ff.Close
		if mf == nil {
			return nil, fmt.Errorf("no arm64 slice in fat binary")
		}
	} else {
		m, merr := macho.Open(appPath)
		if merr != nil {
			return nil, fmt.Errorf("failed to parse Mach-O: %w", merr)
		}
		mf = m
		closeFn = m.Close
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

// buildKBPMPatch mints the signed 128-byte marker (.kbpatch, kind=0) blob.
// newPrice must be exactly 3 ASCII digits. Header layout matches the verified
// engine reader exactly.
func buildKBPMPatch(info *appBinaryInfo, newPrice, privateKeyPath string) ([]byte, error) {
	if len(newPrice) != 3 {
		return nil, fmt.Errorf("new price must be exactly 3 digits (e.g. 080), got %q", newPrice)
	}
	if info.PriceOffset < 0 {
		return nil, fmt.Errorf("price marker %q not found in binary", kbPriceMarker)
	}

	keyBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("private key size wrong: got %d want %d", len(keyBytes), ed25519.PrivateKeySize)
	}

	buf := make([]byte, 128)
	copy(buf[0:4], []byte("KBPM"))
	binary.LittleEndian.PutUint16(buf[4:6], 1)  // version
	binary.LittleEndian.PutUint16(buf[6:8], 64) // header_size
	// buf[8] (kind) left 0 = marker
	copy(buf[16:24], info.BuildID)
	binary.LittleEndian.PutUint64(buf[24:32], uint64(info.PriceOffset)) // slot reused as byte location
	buf[40] = newPrice[0]
	buf[41] = newPrice[1]
	buf[42] = newPrice[2]
	binary.LittleEndian.PutUint32(buf[48:52], 1) // key_id
	binary.LittleEndian.PutUint64(buf[56:64], info.InstrSize)

	sig := ed25519.Sign(ed25519.PrivateKey(keyBytes), buf[0:64])
	copy(buf[64:128], sig)
	return buf, nil
}

// buildWholeBlobPatch mints the signed 128-byte whole-blob (kind=2) header.
// Identity milestone: no diff payload — the engine reconstructs by copying the
// base data+instructions of the running binary (sizes carried here). A real
// diff later appends its payload after byte 128 and adds new-size fields.
//
// Header (signed [0..63]):
//
//	[0..3]   "KBPM"
//	[8]      kind = 2
//	[16..23] build_id
//	[24..31] base_data_size  (LE u64)
//	[56..63] base_instr_size (LE u64)
//	[64..127] Ed25519 signature over [0..63]
func buildWholeBlobPatch(info *appBinaryInfo, privateKeyPath string) ([]byte, error) {
	keyBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("private key size wrong: got %d want %d", len(keyBytes), ed25519.PrivateKeySize)
	}
	if info.InstrSize == 0 || info.DataSize == 0 {
		return nil, fmt.Errorf("instr_size=%d data_size=%d — analysis incomplete", info.InstrSize, info.DataSize)
	}

	buf := make([]byte, 128)
	copy(buf[0:4], []byte("KBPM"))
	binary.LittleEndian.PutUint16(buf[4:6], 1)  // version
	binary.LittleEndian.PutUint16(buf[6:8], 64) // header_size
	buf[8] = 2                                  // kind = whole-blob
	copy(buf[16:24], info.BuildID)
	binary.LittleEndian.PutUint64(buf[24:32], info.DataSize)
	binary.LittleEndian.PutUint32(buf[48:52], 1) // key_id
	binary.LittleEndian.PutUint64(buf[56:64], info.InstrSize)

	sig := ed25519.Sign(ed25519.PrivateKey(keyBytes), buf[0:64])
	copy(buf[64:128], sig)
	return buf, nil
}

// stampBuildId writes the build_id hex into the app bundle so the SDK can
// report it at runtime, at <X.app>/Contents/Resources/koolbase_build_id
// (derived from the App binary path). Writing here does NOT touch the
// instructions section, so the build_id stays valid. Run before code-signing.
// macOS-only for now; iOS/Android land later.
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
