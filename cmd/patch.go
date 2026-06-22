package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var patchCmd = &cobra.Command{
	Use:   "patch",
	Short: "Build, sign and ship VM-level code-push patches",
}

// koolbaseVmDir is the on-device handshake directory shared with the patched
// engine and the Dart SDK: $HOME/Library/Application Support/koolbase/vm.
func koolbaseVmDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not resolve home dir: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "koolbase", "vm"), nil
}

// patchPushCmd builds a kind=3 whole-blob replacement (or kind=4 diff) patch and
// ships it: build → sign → create/match release → create draft → upload → (publish).
//
//	--binary  the CURRENTLY RELEASED App binary (base devices run; build_id source)
//	--new     the recompiled App binary with the changes to ship (payload)
//	--diff    ship a kind=4 KBD1 diff instead of the full kind=3 blob
//
// Works on macOS (App) and Android (libapp.so) — format is auto-detected.
var patchPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Build → sign → upload (→ publish) a whole-blob patch for a built App binary",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		appID, _ := cmd.Flags().GetString("app")
		binary, _ := cmd.Flags().GetString("binary")
		newBin, _ := cmd.Flags().GetString("new")
		keyPath, _ := cmd.Flags().GetString("key")
		channel, _ := cmd.Flags().GetString("channel")
		platform, _ := cmd.Flags().GetString("platform")
		flutterVersion, _ := cmd.Flags().GetString("flutter-version")
		appVersion, _ := cmd.Flags().GetString("app-version")
		matchMode, _ := cmd.Flags().GetString("match-mode")
		releaseID, _ := cmd.Flags().GetString("release")
		rollout, _ := cmd.Flags().GetInt("rollout")
		mandatory, _ := cmd.Flags().GetBool("mandatory")
		notes, _ := cmd.Flags().GetString("notes")
		publish, _ := cmd.Flags().GetBool("publish")
		asDiff, _ := cmd.Flags().GetBool("diff")

		if appID == "" {
			return fmt.Errorf("--app is required")
		}
		if binary == "" {
			return fmt.Errorf("--binary is required (the released/base App binary)")
		}
		asIdentity, _ := cmd.Flags().GetBool("identity")
		if newBin == "" && !asIdentity {
			return fmt.Errorf("--new is required (the recompiled App binary to ship)")
		}

		fmt.Println("  Analyzing base binary...")
		base, err := analyzeAppBinary(binary)
		if err != nil {
			return fmt.Errorf("base analysis failed: %w", err)
		}
		buildID := hex.EncodeToString(base.BuildID)
		fmt.Printf("  ✓ base build_id %s (instr_size %d, data_size %d)\n",
			buildID, base.InstrSize, base.DataSize)

		var blob []byte
		var kindDesc string
		var newData, newInstr []byte
		if !asIdentity {
			fmt.Println("  Extracting new snapshot...")
			var eerr error
			newData, newInstr, eerr = extractSnapshotBlobs(newBin)
			if eerr != nil {
				return fmt.Errorf("new-blob extraction failed: %w", eerr)
			}
			fmt.Printf("  ✓ new snapshot (data %d, instr %d)\n", len(newData), len(newInstr))
		}

		fmt.Println("  Building patch...")
		if asIdentity {
			// kind=2 IDENTITY (internal/dev; engine reuses running base snapshot).
			blob, err = buildWholeBlobPatch(base, keyPath)
			if err != nil {
				return fmt.Errorf("identity patch build failed: %w", err)
			}
			kindDesc = "kind=2 identity"
		} else if asDiff {
			// kind=4 diff: payload = KBD1 delta(baseBlob -> newBlob).
			fmt.Println("  Extracting base snapshot for diff...")
			baseData, baseInstr, berr := extractSnapshotBlobs(binary)
			if berr != nil {
				return fmt.Errorf("base-blob extraction failed: %w", berr)
			}
			fmt.Printf("  ✓ base snapshot (data %d, instr %d)\n", len(baseData), len(baseInstr))
			blob, err = buildDiffPatch(base, baseData, baseInstr, newData, newInstr, keyPath)
			if err != nil {
				return fmt.Errorf("diff patch build failed: %w", err)
			}
			full := len(newData) + len(newInstr)
			fmt.Printf("  ✓ diff payload: %d bytes vs %d full (%.1fx smaller)\n",
				len(blob)-128, full, float64(full)/float64(len(blob)-128))
			kindDesc = "kind=4 diff"
		} else {
			blob, err = buildWholeBlobReplacePatch(base, newData, newInstr, keyPath)
			if err != nil {
				return fmt.Errorf("patch build failed: %w", err)
			}
			kindDesc = "kind=3 replacement"
		}
		checksum := fmt.Sprintf("sha256:%x", sha256.Sum256(blob))
		signature := fmt.Sprintf("%x", blob[64:128])

		tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("kb_%d.kbpatch", time.Now().UnixNano()))
		if err := os.WriteFile(tmpPath, blob, 0644); err != nil {
			return fmt.Errorf("could not write temp patch: %w", err)
		}
		defer os.Remove(tmpPath)
		fmt.Printf("  ✓ patch built (%d bytes, %s)\n", len(blob), kindDesc)

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)

		if releaseID == "" {
			releases, err := client.ListReleases(appID)
			if err != nil {
				return err
			}
			var matched *api.Release
			for _, r := range releases {
				if r.BuildID == buildID && r.Channel == channel && r.Platform == platform {
					rr := r
					matched = &rr
					releaseID = r.ID
					break
				}
			}
			if releaseID == "" {
				rel, err := client.CreateRelease(appID, api.CreateReleaseRequest{
					BuildID:        buildID,
					FlutterVersion: flutterVersion,
					Platform:       platform,
					AppVersion:     appVersion,
					MatchMode:      matchMode,
					Channel:        channel,
				})
				if err != nil {
					return err
				}
				releaseID = rel.ID
				fmt.Printf("  ✓ release created → %s\n", releaseID)
			} else {
				fmt.Printf("  ✓ release matched → %s\n", releaseID)
				printReleaseFlagParity(matched)
			}
		}

		patch, err := client.CreatePatch(appID, releaseID, api.CreatePatchRequest{
			RolloutPercentage: rollout,
			Mandatory:         mandatory,
			ReleaseNotes:      notes,
			Checksum:          checksum,
			Signature:         signature,
			SizeBytes:         len(blob),
		})
		if err != nil {
			return err
		}
		fmt.Printf("  ✓ draft patch #%d → %s\n", patch.PatchNumber, patch.ID)

		fmt.Println("  Uploading artifact...")
		if err := client.UploadPatchArtifact(appID, patch.ID, tmpPath); err != nil {
			return fmt.Errorf("upload failed: %w", err)
		}
		fmt.Println("  ✓ artifact uploaded")

		if publish {
			if err := client.PublishPatch(appID, patch.ID); err != nil {
				return fmt.Errorf("publish failed: %w", err)
			}
			fmt.Printf("\n  Patch #%d LIVE on %s/%s → %d%% of devices\n",
				patch.PatchNumber, platform, channel, rollout)
		} else {
			fmt.Printf("\n  Patch #%d uploaded as DRAFT (not published)\n", patch.PatchNumber)
			fmt.Printf("  Publish with: koolbase patch publish --app %s --patch %s\n", appID, patch.ID)
		}
		fmt.Printf("  Patch ID: %s   build_id: %s\n", patch.ID, buildID)
		return nil
	},
}

// patchStageLocalCmd builds a whole-blob patch and writes it straight to the
// local vm handshake dir as staged.kbpatch — no server, no SDK. Drives the
// engine test directly.
//
//	--binary A            -> kind=2 IDENTITY (engine copies the running base)
//	--binary A --new B     -> kind=3 REPLACEMENT (build_id from A, snapshot from B)
//	--binary A --new B --diff -> kind=4 DIFF (KBD1 delta A->B)
//
// build_id is always computed from --binary (the running/base binary), so it
// must be the exact App the patched engine will run.
var patchStageLocalCmd = &cobra.Command{
	Use:   "stage-local",
	Short: "Build a whole-blob patch and stage it locally (dev/engine test)",
	RunE: func(cmd *cobra.Command, args []string) error {
		binary, _ := cmd.Flags().GetString("binary")
		newBin, _ := cmd.Flags().GetString("new")
		keyPath, _ := cmd.Flags().GetString("key")
		asDiff, _ := cmd.Flags().GetBool("diff")
		if binary == "" {
			return fmt.Errorf("--binary is required (the running/base App binary)")
		}

		fmt.Println("  Analyzing base binary...")
		base, err := analyzeAppBinary(binary)
		if err != nil {
			return fmt.Errorf("base analysis failed: %w", err)
		}
		buildID := hex.EncodeToString(base.BuildID)
		fmt.Printf("  ✓ base build_id %s (instr_size %d, data_size %d)\n",
			buildID, base.InstrSize, base.DataSize)

		var blob []byte
		var kindDesc string
		if newBin == "" {
			// kind=2 identity
			blob, err = buildWholeBlobPatch(base, keyPath)
			if err != nil {
				return fmt.Errorf("patch build failed: %w", err)
			}
			kindDesc = "kind=2 identity"
		} else {
			fmt.Println("  Extracting new snapshot...")
			newData, newInstr, eerr := extractSnapshotBlobs(newBin)
			if eerr != nil {
				return fmt.Errorf("new-blob extraction failed: %w", eerr)
			}
			fmt.Printf("  ✓ new snapshot (data %d, instr %d)\n", len(newData), len(newInstr))
			if asDiff {
				// kind=4 diff: payload = KBD1 delta(baseBlob -> newBlob).
				fmt.Println("  Extracting base snapshot for diff...")
				baseData, baseInstr, berr := extractSnapshotBlobs(binary)
				if berr != nil {
					return fmt.Errorf("base-blob extraction failed: %w", berr)
				}
				fmt.Printf("  ✓ base snapshot (data %d, instr %d)\n", len(baseData), len(baseInstr))
				blob, err = buildDiffPatch(base, baseData, baseInstr, newData, newInstr, keyPath)
				if err != nil {
					return fmt.Errorf("diff patch build failed: %w", err)
				}
				full := len(newData) + len(newInstr)
				fmt.Printf("  ✓ diff payload: %d bytes vs %d full (%.1fx smaller)\n",
					len(blob)-128, full, float64(full)/float64(len(blob)-128))
				kindDesc = "kind=4 diff"
			} else {
				// kind=3 full replacement: payload = new binary's snapshot
				blob, err = buildWholeBlobReplacePatch(base, newData, newInstr, keyPath)
				if err != nil {
					return fmt.Errorf("patch build failed: %w", err)
				}
				kindDesc = "kind=3 replacement"
			}
		}

		vmDir, err := koolbaseVmDir()
		if err != nil {
			return err
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
		fmt.Printf("  base build_id: %s\n", buildID)
		fmt.Println("  Launch the base app; the patched engine reconstructs the snapshot on this run.")
		return nil
	},
}

var patchListCmd = &cobra.Command{
	Use:   "list",
	Short: "List patches for a release",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		appID, _ := cmd.Flags().GetString("app")
		releaseID, _ := cmd.Flags().GetString("release")
		if appID == "" {
			return fmt.Errorf("--app is required")
		}
		if releaseID == "" {
			return fmt.Errorf("--release is required")
		}
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		patches, err := client.ListPatches(appID, releaseID)
		if err != nil {
			return err
		}
		if len(patches) == 0 {
			fmt.Println("No patches found")
			return nil
		}
		fmt.Printf("\n  %-6s %-30s %-14s %-9s %-10s %s\n",
			"PATCH", "ID", "STATUS", "ROLLOUT", "MANDATORY", "CREATED")
		for _, p := range patches {
			fmt.Printf("  #%-5d %-30s %-14s %-9s %-10v %s\n",
				p.PatchNumber, p.ID, statusIcon(p.Status),
				fmt.Sprintf("%d%%", p.RolloutPercentage), p.Mandatory, formatTime(p.CreatedAt))
		}
		fmt.Println()
		return nil
	},
}

var patchPublishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish a draft patch",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		appID, _ := cmd.Flags().GetString("app")
		patchID, _ := cmd.Flags().GetString("patch")
		if appID == "" {
			return fmt.Errorf("--app is required")
		}
		if patchID == "" {
			return fmt.Errorf("--patch is required")
		}
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		if err := client.PublishPatch(appID, patchID); err != nil {
			return err
		}
		fmt.Printf("  ✓ Patch %s published\n", patchID)
		return nil
	},
}

var patchRecallCmd = &cobra.Command{
	Use:   "recall",
	Short: "Recall a published patch",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		appID, _ := cmd.Flags().GetString("app")
		patchID, _ := cmd.Flags().GetString("patch")
		if appID == "" {
			return fmt.Errorf("--app is required")
		}
		if patchID == "" {
			return fmt.Errorf("--patch is required")
		}
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		if err := client.RecallPatch(appID, patchID); err != nil {
			return err
		}
		fmt.Printf("  ✓ Patch %s recalled\n", patchID)
		fmt.Println("  Devices revert to the prior patch on next check")
		return nil
	},
}

func init() {
	patchPushCmd.Flags().String("app", "", "App (project) ID (required)")
	patchPushCmd.Flags().String("binary", "", "Path to the released/base App binary (required)")
	patchPushCmd.Flags().String("new", "", "Path to the recompiled App binary to ship (required)")
	patchPushCmd.Flags().String("key", "private.key", "Path to Ed25519 private key")
	patchPushCmd.Flags().String("channel", "stable", "Release channel")
	patchPushCmd.Flags().String("platform", "macos", "Platform (ios, android, macos)")
	patchPushCmd.Flags().String("flutter-version", "", "Flutter version (used when auto-creating the release)")
	patchPushCmd.Flags().String("app-version", "", "App version (used when auto-creating the release)")
	patchPushCmd.Flags().String("match-mode", "", "Release match mode: build_id (default) or release_version")
	patchPushCmd.Flags().String("release", "", "Explicit release ID (skips build_id match/create)")
	patchPushCmd.Flags().Int("rollout", 100, "Rollout percentage 0-100")
	patchPushCmd.Flags().Bool("mandatory", false, "Mark the patch mandatory (force-update)")
	patchPushCmd.Flags().String("notes", "", "Release notes")
	patchPushCmd.Flags().Bool("publish", false, "Publish immediately after upload")
	patchPushCmd.Flags().Bool("diff", false, "Build a kind=4 DIFF patch (KBD1 delta) instead of kind=3 full")
	patchPushCmd.Flags().Bool("identity", false, "Build a kind=2 IDENTITY patch (internal/dev; engine reuses running base)")

	patchStageLocalCmd.Flags().String("binary", "", "Path to the running/base App binary (required)")
	patchStageLocalCmd.Flags().String("new", "", "Path to a recompiled App binary; present => kind=3 replacement")
	patchStageLocalCmd.Flags().Bool("diff", false, "With --new: build a kind=4 DIFF patch (KBD1 delta) instead of kind=3 full")
	patchStageLocalCmd.Flags().String("key", "private.key", "Path to Ed25519 private key")

	patchListCmd.Flags().String("app", "", "App (project) ID (required)")
	patchListCmd.Flags().String("release", "", "Release ID (required)")

	patchPublishCmd.Flags().String("app", "", "App (project) ID (required)")
	patchPublishCmd.Flags().String("patch", "", "Patch ID (required)")

	patchRecallCmd.Flags().String("app", "", "App (project) ID (required)")
	patchRecallCmd.Flags().String("patch", "", "Patch ID (required)")

	patchCmd.AddCommand(patchPushCmd)
	patchCmd.AddCommand(patchStageLocalCmd)
	patchCmd.AddCommand(patchListCmd)
	patchCmd.AddCommand(patchPublishCmd)
	patchCmd.AddCommand(patchRecallCmd)
	rootCmd.AddCommand(patchCmd)
}
