// cmd/patch_build.go
package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"os"
)

// appBinaryInfo is the result of analysing a built Flutter App binary for
// VM-level (System B) patching: the build_id the engine verifies against, the
// size of the isolate-instructions region that build_id was hashed over, and
// the file offset of the price marker's 3 ASCII digits.
//
// Faithful port of the verified writer_macho_v2 tool — same loader
// (OpenFat → arm64 slice), same symbol (_kDartIsolateSnapshotInstructions),
// same section-based hash range — so the build_id is byte-for-byte what the
// patched engine recomputes at apply time. Do NOT "optimise" the hash range:
// it must stay the section slice from the symbol to the end of the section.
type appBinaryInfo struct {
	BuildID     []byte // SHA-256(instructions)[0:8]
	InstrSize   uint64 // bytes the engine must hash
	PriceOffset int64  // file offset of the 3 price digits
}

const (
	kbInstrSym    = "_kDartIsolateSnapshotInstructions"
	kbPriceMarker = "KBPRICE@@@100@@@END"
)

// analyzeAppBinary inspects a built Flutter App Mach-O and returns the data
// needed to mint a .kbpatch for it.
func analyzeAppBinary(appPath string) (*appBinaryInfo, error) {
	data, err := os.ReadFile(appPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read binary: %w", err)
	}

	// Locate the price marker; the 3 digits sit right after "KBPRICE@@@".
	marker := []byte(kbPriceMarker)
	offset := -1
	for i := 0; i+len(marker) <= len(data); i++ {
		if bytes.Equal(data[i:i+len(marker)], marker) {
			offset = i
			break
		}
	}
	if offset == -1 {
		return nil, fmt.Errorf("price marker %q not found in binary", kbPriceMarker)
	}
	priceOffset := int64(offset + len("KBPRICE@@@"))

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

	// Find the isolate-instructions symbol.
	var symVA uint64
	found := false
	if mf.Symtab != nil {
		for _, s := range mf.Symtab.Syms {
			if s.Name == kbInstrSym {
				symVA = s.Value
				found = true
				break
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("symbol %s not found", kbInstrSym)
	}

	// Find the section containing the symbol, then hash from the symbol to
	// the end of that section — the exact range the engine recomputes.
	var sec *macho.Section
	for _, s := range mf.Sections {
		if symVA >= s.Addr && symVA < s.Addr+s.Size {
			sec = s
			break
		}
	}
	if sec == nil {
		return nil, fmt.Errorf("no section contains 0x%x", symVA)
	}
	secData, derr := sec.Data()
	if derr != nil {
		return nil, fmt.Errorf("failed to read section data: %w", derr)
	}
	startInSec := symVA - sec.Addr
	if startInSec > uint64(len(secData)) {
		return nil, fmt.Errorf("symbol offset 0x%x beyond section data 0x%x", startInSec, len(secData))
	}
	instrBytes := secData[startInSec:]
	sum := sha256.Sum256(instrBytes)

	return &appBinaryInfo{
		BuildID:     sum[:8],
		InstrSize:   uint64(len(instrBytes)),
		PriceOffset: priceOffset,
	}, nil
}

// buildKBPMPatch mints the signed 128-byte .kbpatch blob for a binary that
// analyzeAppBinary already inspected. newPrice must be exactly 3 ASCII digits.
// Header layout matches the verified engine reader exactly.
func buildKBPMPatch(info *appBinaryInfo, newPrice, privateKeyPath string) ([]byte, error) {
	if len(newPrice) != 3 {
		return nil, fmt.Errorf("new price must be exactly 3 digits (e.g. 080), got %q", newPrice)
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
	copy(buf[16:24], info.BuildID)
	binary.LittleEndian.PutUint64(buf[24:32], uint64(info.PriceOffset)) // slot_index reused as byte location
	buf[40] = newPrice[0]
	buf[41] = newPrice[1]
	buf[42] = newPrice[2]
	binary.LittleEndian.PutUint32(buf[48:52], 1) // key_id
	binary.LittleEndian.PutUint64(buf[56:64], info.InstrSize)

	sig := ed25519.Sign(ed25519.PrivateKey(keyBytes), buf[0:64])
	copy(buf[64:128], sig)

	return buf, nil
}
