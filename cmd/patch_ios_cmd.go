package cmd

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// packKBPI assembles the KBPI container from a compiled .bytecode file and a
// key file. Per STEP5_KBPI_ASSEMBLY_DECISION.md the container is assembled here
// in the CLI, not in dart2bytecode.
//
// KBPI := "KBPI" | u16 ver=1 | u16 rsvd | u32 bc_len | bytecode
//
//	| u32 rows | rows×( u32 name_len | name utf8 | key[32] )
//
// v1 reads the TEXT key file that KOOLBASE_KEY_OUT already emits — lines of
//
//	<uri|scope|kind|name>\t<64-hex-char key>
//
// (a proper binary sidecar from dart2bytecode is a later cleanup; the text file
// carries the same information and is already produced, so the CLI is provable
// against the device-proven path with zero compiler change). The module
// entrypoint 'main' is skipped (it carries no override; the apply loop skips it).
func packKBPI(bytecodePath, keysPath string) ([]byte, error) {
	bytecode, err := os.ReadFile(bytecodePath)
	if err != nil {
		return nil, fmt.Errorf("read bytecode: %w", err)
	}

	type keyRow struct {
		name string
		key  []byte // 32 bytes
	}
	var rows []keyRow

	kf, err := os.Open(keysPath)
	if err != nil {
		return nil, fmt.Errorf("read keys: %w", err)
	}
	defer kf.Close()

	sc := bufio.NewScanner(kf)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\n")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			continue
		}

		prefix, keyHex := parts[0], strings.TrimSpace(parts[1])
		// prefix = uri|scope|kind|name. Carrier VM name convention:
		//   scope == "TOP"  -> bare name          (top-level fn)
		//   scope == class  -> "<scope>.<name>"   (hoisted method, koolbase-patch-mode)
		seg := strings.Split(prefix, "|")
		if len(seg) != 4 {
			return nil, fmt.Errorf("bad keytable prefix %q", prefix)
		}
		scope, name := seg[1], seg[3]
		if scope == "TOP" {
			if name == "main" { // module entrypoint, not an override target
				continue
			}
		} else {
			name = scope + "." + name
		}

		key, derr := hex.DecodeString(keyHex)
		if derr != nil || len(key) != 32 {
			return nil, fmt.Errorf("bad key for %q: %d bytes", name, len(key))
		}
		rows = append(rows, keyRow{name: name, key: key})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan keys: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no key rows selected from %s", keysPath)
	}

	var out []byte
	out = append(out, []byte("KBPI")...)
	out = binary.LittleEndian.AppendUint16(out, 1) // version
	out = binary.LittleEndian.AppendUint16(out, 0) // reserved
	out = binary.LittleEndian.AppendUint32(out, uint32(len(bytecode)))
	out = append(out, bytecode...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(rows)))
	for _, r := range rows {
		nb := []byte(r.name)
		out = binary.LittleEndian.AppendUint32(out, uint32(len(nb)))
		out = append(out, nb...)
		out = append(out, r.key...)
	}
	return out, nil
}

// patchIosCmd builds an iOS bytecode code-push patch. Pre-built inputs only
// (--bytecode + --keys); the packer never invokes dart2bytecode.
//
//	default : signed KBPM kind=5 envelope wrapping the KBPI container
//	--raw   : bare KBPI container (the device-proven apply path; used for the
//	          end-to-end CLI test before the native KBPM-unwrap exists)
var patchIosCmd = &cobra.Command{
	Use:   "ios",
	Short: "Build an iOS bytecode (KBPI) code-push patch",
	RunE: func(cmd *cobra.Command, args []string) error {
		bytecodePath, _ := cmd.Flags().GetString("bytecode")
		keysPath, _ := cmd.Flags().GetString("keys")
		binary, _ := cmd.Flags().GetString("binary")
		keyPath, _ := cmd.Flags().GetString("key")
		stageLocal, _ := cmd.Flags().GetBool("stage-local")
		raw, _ := cmd.Flags().GetBool("raw")

		kbpiPath, _ := cmd.Flags().GetString("kbpi")
		if kbpiPath == "" {
			if bytecodePath == "" {
				return fmt.Errorf("--bytecode is required (or use --kbpi to wrap a pre-built container)")
			}
			if keysPath == "" {
				return fmt.Errorf("--keys is required (or use --kbpi)")
			}
		}

		allowUnsafe, _ := cmd.Flags().GetBool("allow-unsafe")
		if !allowUnsafe {
			bc, rerr := os.ReadFile(bytecodePath)
			if rerr != nil {
				return fmt.Errorf("read bytecode for safety check: %w", rerr)
			}
			if ferr := fenceUnsafePatterns(bc); ferr != nil {
				return fmt.Errorf("patch rejected by safety fence: %w (use --allow-unsafe to override, debugging only)", ferr)
			}
		}

		var kbpi []byte
		var err error
		if kbpiPath != "" {
			kbpi, err = os.ReadFile(kbpiPath)
			if err != nil {
				return fmt.Errorf("read --kbpi: %w", err)
			}
			fmt.Printf("  ✓ using pre-built KBPI %s (%d bytes)\n", kbpiPath, len(kbpi))
		} else {
			fmt.Println("  Packing KBPI container...")
			kbpi, err = packKBPI(bytecodePath, keysPath)
			if err != nil {
				return fmt.Errorf("pack KBPI failed: %w", err)
			}
			fmt.Printf("  ✓ KBPI container (%d bytes)\n", len(kbpi))
		}

		var blob []byte
		var kindDesc string
		if raw {
			blob = kbpi
			kindDesc = "raw KBPI (device-proven path)"
		} else {
			if binary == "" {
				return fmt.Errorf("--binary is required for the signed KBPM envelope (use --raw to skip)")
			}
			fmt.Println("  Analyzing base binary...")
			base, aerr := analyzeAppBinary(binary)
			if aerr != nil {
				return fmt.Errorf("base analysis failed: %w", aerr)
			}
			fmt.Printf("  ✓ base build_id %s (instr_size %d)\n",
				hex.EncodeToString(base.BuildID), base.InstrSize)
			blob, err = buildKBPIPatch(base, kbpi, keyPath)
			if err != nil {
				return fmt.Errorf("KBPM envelope build failed: %w", err)
			}
			kindDesc = "kind=5 KBPM envelope (signed)"
		}

		if stageLocal {
			vmDir, verr := koolbaseVmDir()
			if verr != nil {
				return verr
			}
			if err := os.MkdirAll(vmDir, 0o755); err != nil {
				return fmt.Errorf("could not create vm dir: %w", err)
			}
			_ = os.Remove(filepath.Join(vmDir, "applied.kbpatch"))
			stagedPath := filepath.Join(vmDir, "staged.kbpatch")
			if err := os.WriteFile(stagedPath, blob, 0o644); err != nil {
				return fmt.Errorf("could not stage patch: %w", err)
			}
			fmt.Printf("  ✓ staged %s (%d bytes) → %s\n", kindDesc, len(blob), stagedPath)
			return nil
		}

		// No --stage-local: write next to the bytecode for inspection / manual
		// delivery. Server upload/publish wiring mirrors `patch push` and is a
		// follow-up (kept out of v1 to keep the packer self-contained).
		outPath := strings.TrimSuffix(bytecodePath, filepath.Ext(bytecodePath)) + ".kbpatch"
		if err := os.WriteFile(outPath, blob, 0o644); err != nil {
			return fmt.Errorf("write patch: %w", err)
		}
		fmt.Printf("  ✓ wrote %s (%d bytes) → %s\n", kindDesc, len(blob), outPath)
		// checksum for the delivery layer (matches `patch push` semantics).
		sum := sha256.Sum256(blob)
		fmt.Printf("  checksum: %s\n", hex.EncodeToString(sum[:]))
		return nil
	},
}
