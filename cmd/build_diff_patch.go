package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// buildDiffPatch mints a signed kind=4 (diff) patch. Same 128-byte signed header
// as kind=3, but the payload is a KBD1 delta (baseBlob -> newBlob) rather than the
// full new blob. The engine reconstructs newBlob = kbpatch(baseBlob, delta), then
// proceeds exactly as kind=3 from there.
//
//	[8]       kind = 4
//	[16..23]  build_id (base) — pins to the running base binary
//	[24..31]  len(newData)    — RECONSTRUCTED data length (engine splits by this)
//	[32..39]  len(newInstr)   — RECONSTRUCTED instr length
//	[40..55]  SHA-256(newData || newInstr)[0:16]  — hash of the RECONSTRUCTED
//	          TARGET (not the diff). Engine verifies kbpatch output against this,
//	          so a patcher bug / format mismatch / wrong base is caught before load.
//	[56..63]  base instr_size (for the build_id check)
//	[64..127] Ed25519 sig over [0..63]
//	[128..]   KBD1 delta payload
//
// baseData/baseInstr must be extracted from the BASE binary with the SAME
// extractor (extractSnapshotBlobs) used for newData/newInstr, so the blob the
// engine reconstructs and splits lines up byte-for-byte.
func buildDiffPatch(base *appBinaryInfo, baseData, baseInstr, newData, newInstr []byte, privateKeyPath string) ([]byte, error) {
	keyBytes, err := loadKey(privateKeyPath)
	if err != nil {
		return nil, err
	}
	if len(newData) == 0 || len(newInstr) == 0 {
		return nil, fmt.Errorf("empty new blobs: data=%d instr=%d", len(newData), len(newInstr))
	}
	if len(baseData) == 0 || len(baseInstr) == 0 {
		return nil, fmt.Errorf("empty base blobs: data=%d instr=%d", len(baseData), len(baseInstr))
	}
	if base.InstrSize == 0 {
		return nil, fmt.Errorf("base instr_size is 0 — base analysis incomplete")
	}

	// Concatenate in the SAME order the engine reconstructs+splits: data || instr.
	baseBlob := make([]byte, 0, len(baseData)+len(baseInstr))
	baseBlob = append(baseBlob, baseData...)
	baseBlob = append(baseBlob, baseInstr...)

	newBlob := make([]byte, 0, len(newData)+len(newInstr))
	newBlob = append(newBlob, newData...)
	newBlob = append(newBlob, newInstr...)

	// Hash of the RECONSTRUCTED target (Kennedy's correctness guarantee).
	targetHash := sha256.Sum256(newBlob)

	// The diff payload.
	delta := kbdiffEncode(baseBlob, newBlob, len(baseData), len(baseInstr))

	// Self-verify: reconstruct from our own delta and confirm it byte-matches the
	// target. A diff that can't be re-applied to produce the exact target must
	// never ship — this catches differ bugs at mint time, before the device.
	recon, rerr := kbpatchDecode(baseBlob, delta)
	if rerr != nil {
		return nil, fmt.Errorf("diff self-verify (decode) failed: %w", rerr)
	}
	if !bytesEqual(recon, newBlob) {
		return nil, fmt.Errorf("diff self-verify FAILED: reconstructed blob != target (len %d vs %d)", len(recon), len(newBlob))
	}

	// Heads-up if the diff barely compressed. A delta that is a large fraction of
	// the full snapshot means base and new share almost no bytes — in practice
	// that's --new built with different flags than the released base (--flavor /
	// --dart-define / --obfuscate / --target all reshuffle bytes app-wide), which
	// is the gap left by patch's binary-in design. Non-fatal: a genuine large
	// change trips it too, so it's a heads-up, not a hard error.
	warnIfLargeDiff(len(delta), len(newBlob))

	header := make([]byte, 128)
	copy(header[0:4], []byte("KBPM"))
	binary.LittleEndian.PutUint16(header[4:6], 1)
	binary.LittleEndian.PutUint16(header[6:8], 64)
	header[8] = 4 // kind = diff
	copy(header[16:24], base.BuildID)
	binary.LittleEndian.PutUint64(header[24:32], uint64(len(newData)))
	binary.LittleEndian.PutUint64(header[32:40], uint64(len(newInstr)))
	copy(header[40:56], targetHash[:16])
	binary.LittleEndian.PutUint64(header[56:64], base.InstrSize)
	sig := ed25519.Sign(ed25519.PrivateKey(keyBytes), header[0:64])
	copy(header[64:128], sig)

	out := make([]byte, 0, 128+len(delta))
	out = append(out, header...)
	out = append(out, delta...)
	return out, nil
}

// largeDiffWarnFraction is the delta/full ratio at or above which buildDiffPatch
// warns. Below it (the diff is at least 2x smaller than the full snapshot) is the
// normal range for a real code change; at/above it the base and new binaries share
// almost no bytes, which in practice means a build-flag mismatch on --new.
const largeDiffWarnFraction = 0.5

// warnIfLargeDiff prints a non-fatal heads-up when a kind=4 delta is an unusually
// large fraction of the full snapshot it reconstructs.
func warnIfLargeDiff(deltaLen, fullLen int) {
	if fullLen <= 0 || deltaLen <= 0 {
		return
	}
	frac := float64(deltaLen) / float64(fullLen)
	if frac < largeDiffWarnFraction {
		return
	}
	fmt.Printf("\n  \u26a0 diff is %.0f%% of the full snapshot (only %.1fx smaller) — unusually large.\n",
		frac*100, float64(fullLen)/float64(deltaLen))
	fmt.Println("    The usual cause is --new built with different flags than the released")
	fmt.Println("    base: --flavor / --dart-define / --obfuscate / --target.")
	fmt.Println("    Rebuild --new with the SAME flags as the release, then re-run.")
	fmt.Println("    (Disregard if you genuinely shipped a large change.)")
}

func bytesEqual(a, b []byte) bool { return bytes.Equal(a, b) }
