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

var patchPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Build → sign → upload (→ publish) a patch for a built App binary",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		appID, _ := cmd.Flags().GetString("app")
		binary, _ := cmd.Flags().GetString("binary")
		newPrice, _ := cmd.Flags().GetString("new-price")
		keyPath, _ := cmd.Flags().GetString("key")
		channel, _ := cmd.Flags().GetString("channel")
		platform, _ := cmd.Flags().GetString("platform")
		flutterVersion, _ := cmd.Flags().GetString("flutter-version")
		appVersion, _ := cmd.Flags().GetString("app-version")
		releaseID, _ := cmd.Flags().GetString("release")
		rollout, _ := cmd.Flags().GetInt("rollout")
		mandatory, _ := cmd.Flags().GetBool("mandatory")
		notes, _ := cmd.Flags().GetString("notes")
		publish, _ := cmd.Flags().GetBool("publish")

		if appID == "" {
			return fmt.Errorf("--app is required")
		}
		if binary == "" {
			return fmt.Errorf("--binary is required (path to the built App binary)")
		}
		if len(newPrice) != 3 {
			return fmt.Errorf("--new-price must be exactly 3 digits (e.g. 080)")
		}

		fmt.Println("  Analyzing binary...")
		info, err := analyzeAppBinary(binary)
		if err != nil {
			return fmt.Errorf("analysis failed: %w", err)
		}
		buildID := hex.EncodeToString(info.BuildID)
		fmt.Printf("  ✓ build_id %s (instr_size %d)\n", buildID, info.InstrSize)

		if stamped, serr := stampBuildId(binary, buildID); serr != nil {
			fmt.Printf("  ! build_id not stamped into bundle: %v\n", serr)
		} else {
			fmt.Printf("  ✓ stamped build_id → %s\n", stamped)
		}

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)

		// Resolve release: explicit flag, else match by build_id, else create.
		if releaseID == "" {
			releases, err := client.ListReleases(appID)
			if err != nil {
				return err
			}
			for _, r := range releases {
				if r.BuildID == buildID && r.Channel == channel && r.Platform == platform {
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
					Channel:        channel,
				})
				if err != nil {
					return err
				}
				releaseID = rel.ID
				fmt.Printf("  ✓ release created → %s\n", releaseID)
			} else {
				fmt.Printf("  ✓ release matched → %s\n", releaseID)
			}
		}

		fmt.Println("  Building patch...")
		blob, err := buildKBPMPatch(info, newPrice, keyPath)
		if err != nil {
			return fmt.Errorf("patch build failed: %w", err)
		}
		checksum := fmt.Sprintf("sha256:%x", sha256.Sum256(blob))
		signature := fmt.Sprintf("%x", blob[64:128])

		tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("kb_%d.kbpatch", time.Now().UnixNano()))
		if err := os.WriteFile(tmpPath, blob, 0644); err != nil {
			return fmt.Errorf("could not write temp patch: %w", err)
		}
		defer os.Remove(tmpPath)
		fmt.Printf("  ✓ patch built (%d bytes)\n", len(blob))

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

// patchStageLocalCmd builds a whole-blob (kind=2) patch for a built App binary
// and writes it straight to the local vm handshake dir as staged.kbpatch. No
// server, no SDK — this drives the engine identity-load test directly, the same
// way the marker demo was staged. The build_id is computed from THIS binary, so
// it must be the exact App the patched engine will run.
var patchStageLocalCmd = &cobra.Command{
	Use:   "stage-local",
	Short: "Build a whole-blob patch for a built App and stage it locally (dev/engine test)",
	RunE: func(cmd *cobra.Command, args []string) error {
		binary, _ := cmd.Flags().GetString("binary")
		keyPath, _ := cmd.Flags().GetString("key")
		if binary == "" {
			return fmt.Errorf("--binary is required (path to the built App binary)")
		}

		fmt.Println("  Analyzing binary...")
		info, err := analyzeAppBinary(binary)
		if err != nil {
			return fmt.Errorf("analysis failed: %w", err)
		}
		buildID := hex.EncodeToString(info.BuildID)
		fmt.Printf("  ✓ build_id %s (instr_size %d, data_size %d)\n",
			buildID, info.InstrSize, info.DataSize)

		blob, err := buildWholeBlobPatch(info, keyPath)
		if err != nil {
			return fmt.Errorf("patch build failed: %w", err)
		}

		vmDir, err := koolbaseVmDir()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(vmDir, 0o755); err != nil {
			return fmt.Errorf("could not create vm dir: %w", err)
		}
		// Clear any prior applied marker so this is a clean run.
		_ = os.Remove(filepath.Join(vmDir, "applied.kbpatch"))

		stagedPath := filepath.Join(vmDir, "staged.kbpatch")
		if err := os.WriteFile(stagedPath, blob, 0o644); err != nil {
			return fmt.Errorf("could not stage patch: %w", err)
		}

		fmt.Printf("  ✓ staged whole-blob patch (%d bytes, kind=2) → %s\n", len(blob), stagedPath)
		fmt.Printf("  build_id: %s\n", buildID)
		fmt.Println("  Launch the app; the patched engine reconstructs the snapshot on this run.")
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
	patchPushCmd.Flags().String("binary", "", "Path to the built App binary (required)")
	patchPushCmd.Flags().String("new-price", "", "New 3-digit price to patch in, e.g. 080 (required)")
	patchPushCmd.Flags().String("key", "private.key", "Path to Ed25519 private key")
	patchPushCmd.Flags().String("channel", "stable", "Release channel")
	patchPushCmd.Flags().String("platform", "macos", "Platform (ios, android, macos)")
	patchPushCmd.Flags().String("flutter-version", "", "Flutter version (used when auto-creating the release)")
	patchPushCmd.Flags().String("app-version", "", "App version (used when auto-creating the release)")
	patchPushCmd.Flags().String("release", "", "Explicit release ID (skips build_id match/create)")
	patchPushCmd.Flags().Int("rollout", 100, "Rollout percentage 0-100")
	patchPushCmd.Flags().Bool("mandatory", false, "Mark the patch mandatory (force-update)")
	patchPushCmd.Flags().String("notes", "", "Release notes")
	patchPushCmd.Flags().Bool("publish", false, "Publish immediately after upload")

	patchStageLocalCmd.Flags().String("binary", "", "Path to the built App binary (required)")
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
