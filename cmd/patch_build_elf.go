package cmd

import (
	"crypto/sha256"
	"debug/elf"
	"fmt"
)

// ELF (Android libapp.so) binary reading for VM-level (System B) patching —
// the counterpart to the Mach-O primitives in patch_build.go. Proven on a real
// Android device (Phase 2/4): libapp.so carries the snapshot symbols in .dynsym
// with the leading-underscore names, and populates st_size, so we slice exactly
// [symVA, symVA+st_size) from the containing PROGBITS section.

// isELF reports whether the file at path begins with the ELF magic (0x7f 'E'
// 'L' 'F'). Used to dispatch analyze/extract between Mach-O and ELF.
func isELF(head []byte) bool {
	return len(head) >= 4 &&
		head[0] == 0x7f && head[1] == 'E' && head[2] == 'L' && head[3] == 'F'
}

// isMachO reports whether the head looks like a Mach-O (thin or fat/universal).
func isMachO(head []byte) bool {
	if len(head) < 4 {
		return false
	}
	be := uint32(head[0])<<24 | uint32(head[1])<<16 | uint32(head[2])<<8 | uint32(head[3])
	switch be {
	case 0xFEEDFACE, 0xFEEDFACF, // thin 32/64 (BE view of LE magic)
		0xCEFAEDFE, 0xCFFAEDFE, // thin 32/64 little-endian
		0xCAFEBABE, 0xBEBAFECA: // fat/universal
		return true
	}
	return false
}

// elfSymbolBytes returns the bytes of the named symbol from an ELF, sliced by
// the symbol's own st_size out of its containing PROGBITS section. Lifted from
// the proven writer_elf reference.
func elfSymbolBytes(f *elf.File, symName string) ([]byte, error) {
	syms, err := f.DynamicSymbols()
	if err != nil {
		return nil, fmt.Errorf("read .dynsym: %w", err)
	}
	var symVA, symSize uint64
	found := false
	for _, sym := range syms {
		if sym.Name == symName {
			symVA = sym.Value
			symSize = sym.Size
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("symbol %s not found in .dynsym", symName)
	}
	if symSize == 0 {
		return nil, fmt.Errorf("symbol %s has zero st_size", symName)
	}

	var sec *elf.Section
	for _, sc := range f.Sections {
		if sc.Type == elf.SHT_NOBITS {
			continue
		}
		if sc.Addr != 0 && symVA >= sc.Addr && symVA < sc.Addr+sc.Size {
			sec = sc
			break
		}
	}
	if sec == nil {
		return nil, fmt.Errorf("no section contains VA 0x%x for %s", symVA, symName)
	}
	secData, derr := sec.Data()
	if derr != nil {
		return nil, fmt.Errorf("read section %s: %w", sec.Name, derr)
	}
	start := symVA - sec.Addr
	end := start + symSize
	if end > uint64(len(secData)) {
		return nil, fmt.Errorf("symbol %s range [0x%x,0x%x) exceeds section data 0x%x",
			symName, start, end, len(secData))
	}
	return secData[start:end], nil
}

// analyzeELF inspects an Android libapp.so and returns the patch-minting info.
func analyzeELF(appPath string) (*appBinaryInfo, error) {
	f, err := elf.Open(appPath)
	if err != nil {
		return nil, fmt.Errorf("open elf: %w", err)
	}
	defer f.Close()

	instr, err := elfSymbolBytes(f, kbInstrSym)
	if err != nil {
		return nil, err
	}
	data, err := elfSymbolBytes(f, kbDataSym)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(instr)
	return &appBinaryInfo{
		BuildID:     sum[:8],
		InstrSize:   uint64(len(instr)),
		DataSize:    uint64(len(data)),
		PriceOffset: -1, // price marker is a Mach-O demo concept; N/A for ELF
	}, nil
}

// extractSnapshotBlobsELF returns the isolate data + instructions blobs from an
// Android libapp.so — the payload of a kind=3 whole-blob replacement patch.
func extractSnapshotBlobsELF(appPath string) (data, instr []byte, err error) {
	f, oerr := elf.Open(appPath)
	if oerr != nil {
		return nil, nil, fmt.Errorf("open elf: %w", oerr)
	}
	defer f.Close()
	data, err = elfSymbolBytes(f, kbDataSym)
	if err != nil {
		return nil, nil, err
	}
	instr, err = elfSymbolBytes(f, kbInstrSym)
	if err != nil {
		return nil, nil, err
	}
	return data, instr, nil
}
