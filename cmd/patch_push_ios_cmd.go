package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
)

// patch push-ios: publish a pre-built iOS KBC bytecode container.
//
// The iOS artifact is a KBC container produced by `koolbase patch ios`. The
// server treats artifacts as opaque bytes, so this command is pure publish
// plumbing: CreateRelease (or --release) -> CreatePatch(draft, checksum inline)
// -> UploadPatchArtifact -> PublishPatch (unless --no-publish).
//
// Match modes:
//
//	build_id (default)     — strong content-hash match; needs --build-id
//	                         (the value koolbaseBuildId() reports on device).
//	release_version        — store-version match; needs --app-version.
var patchPushIOSCmd = &cobra.Command{
	Use:   "push-ios",
	Short: "Upload and publish a pre-built iOS .kbc patch container",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		appID, _ := cmd.Flags().GetString("app")
		container, _ := cmd.Flags().GetString("container")
		releaseID, _ := cmd.Flags().GetString("release")
		appVersion, _ := cmd.Flags().GetString("app-version")
		flutterVersion, _ := cmd.Flags().GetString("flutter-version")
		channel, _ := cmd.Flags().GetString("channel")
		rollout, _ := cmd.Flags().GetInt("rollout")
		mandatory, _ := cmd.Flags().GetBool("mandatory")
		notes, _ := cmd.Flags().GetString("notes")
		noPublish, _ := cmd.Flags().GetBool("no-publish")
		buildID, _ := cmd.Flags().GetString("build-id")

		matchMode, _ := cmd.Flags().GetString("match-mode")
		if matchMode == "" {
			matchMode = "build_id" // strong content-hash match by default
		}

		if appID == "" {
			return fmt.Errorf("--app is required")
		}
		kbpiPath, _ := cmd.Flags().GetString("kbpi")
		binaryPath, _ := cmd.Flags().GetString("binary")
		keyPath, _ := cmd.Flags().GetString("key")

		var blob []byte
		if kbpiPath != "" {
			// One-command authoring path: pack the KBPI into a signed KBPM
			// here (same analyzeAppBinary + buildKBPIPatch as `patch ios`)
			// and derive the release build_id from the binary — no hand-typed
			// --build-id, no separate wrap step.
			if binaryPath == "" || keyPath == "" {
				return fmt.Errorf("--kbpi requires --binary (base App) and --key (Ed25519 private key)")
			}
			kbpi, rerr := os.ReadFile(kbpiPath)
			if rerr != nil {
				return fmt.Errorf("could not read kbpi: %w", rerr)
			}
			fmt.Println("  Analyzing base binary...")
			base, aerr := analyzeAppBinary(binaryPath)
			if aerr != nil {
				return fmt.Errorf("base analysis failed: %w", aerr)
			}
			derived := hex.EncodeToString(base.BuildID)
			if buildID != "" && buildID != derived {
				return fmt.Errorf("--build-id %s does not match the binary's build_id %s (omit --build-id; it is derived)", buildID, derived)
			}
			buildID = derived
			fmt.Printf("  ✓ base build_id %s (derived from binary)\n", buildID)
			blob, err = buildKBPIPatch(base, kbpi, keyPath)
			if err != nil {
				return fmt.Errorf("KBPM envelope build failed: %w", err)
			}
			fmt.Printf("  ✓ signed KBPM envelope (%d bytes)\n", len(blob))
			// The uploader is file-path based; persist the envelope next to the
			// KBPI (customer keeps the artifact, matching `patch ios` output).
			container = strings.TrimSuffix(kbpiPath, filepath.Ext(kbpiPath)) + ".kbpatch"
			if werr := os.WriteFile(container, blob, 0o644); werr != nil {
				return fmt.Errorf("could not write KBPM: %w", werr)
			}
			fmt.Printf("  ✓ wrote %s\n", container)
		} else {
			if container == "" {
				return fmt.Errorf("--container or --kbpi is required")
			}
			blob, err = os.ReadFile(container)
			if err != nil {
				return fmt.Errorf("could not read container: %w", err)
			}
		}
		// KBPM envelopes carry the Ed25519 signature at bytes 64:128 — record
		// it on the patch row (mirrors Android patch push) so the field is
		// uniformly meaningful instead of empty on iOS.
		var sigHex string
		if len(blob) >= 128 && string(blob[0:4]) == "KBPM" {
			sigHex = fmt.Sprintf("%x", blob[64:128])
		}
		checksum := fmt.Sprintf("sha256:%x", sha256.Sum256(blob))
		fmt.Printf("  artifact %s (%d bytes)\n  checksum %s\n", container, len(blob), checksum)

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)

		// Release: use existing, or create one in the selected match mode.
		if releaseID == "" {
			if matchMode == "release_version" && appVersion == "" {
				return fmt.Errorf("--app-version is required for release_version mode")
			}
			if matchMode == "build_id" && buildID == "" {
				return fmt.Errorf("--build-id is required for build_id mode (from koolbaseBuildId() on device)")
			}
			// app_version gates client compatibility; in build_id mode the flag
			// is optional, so fall back to pubspec.yaml (mirrors koolbase
			// release for Android) and warn if neither is available.
			if appVersion == "" {
				if appVersion = readPubspecVersion("."); appVersion != "" {
					fmt.Printf("  ✓ app_version %s (from pubspec.yaml)\n", appVersion)
				} else {
					fmt.Fprintln(os.Stderr, "  ⚠ registering release without app_version — pass --app-version or run from the app directory (pubspec.yaml)")
				}
			}
			rel, rerr := client.CreateRelease(appID, api.CreateReleaseRequest{
				BuildID:        buildID,
				Platform:       "ios",
				AppVersion:     appVersion,
				FlutterVersion: flutterVersion,
				MatchMode:      matchMode,
				Channel:        channel,
			})
			if rerr != nil {
				return fmt.Errorf("create release failed: %w", rerr)
			}
			releaseID = rel.ID
			fmt.Printf("  ✓ release created %s (ios, mode=%s, channel %s)\n", releaseID, matchMode, channel)
		}

		patch, err := client.CreatePatch(appID, releaseID, api.CreatePatchRequest{
			ReleaseID:         releaseID,
			RolloutPercentage: rollout,
			Mandatory:         mandatory,
			ReleaseNotes:      notes,
			Checksum:          checksum,
			Signature:         sigHex,
			SizeBytes:         len(blob),
		})
		if err != nil {
			return fmt.Errorf("create patch failed: %w", err)
		}
		fmt.Printf("  ✓ draft patch %s (number %d)\n", patch.ID, patch.PatchNumber)

		if err := client.UploadPatchArtifact(appID, patch.ID, container); err != nil {
			return fmt.Errorf("upload failed: %w", err)
		}
		fmt.Println("  ✓ artifact uploaded")

		if noPublish {
			fmt.Println("  draft left unpublished (--no-publish)")
			return nil
		}
		if err := client.PublishPatch(appID, patch.ID); err != nil {
			return fmt.Errorf("publish failed: %w", err)
		}
		fmt.Printf("  ✓ PUBLISHED patch %d on channel %s\n", patch.PatchNumber, channel)
		return nil
	},
}

func init() {
	patchPushIOSCmd.Flags().String("app", "", "App/project ID")
	patchPushIOSCmd.Flags().String("container", "", "Path to a pre-built signed .kbpatch/.kbc container")
	patchPushIOSCmd.Flags().String("kbpi", "", "Path to a KBPI (from `patch ios build`) — signs + publishes in one command")
	patchPushIOSCmd.Flags().String("binary", "", "Base App binary (required with --kbpi; build_id source)")
	patchPushIOSCmd.Flags().String("key", "", "Ed25519 private key (required with --kbpi)")
	patchPushIOSCmd.Flags().String("release", "", "Existing release ID (skips release creation)")
	patchPushIOSCmd.Flags().String("app-version", "", "Release version to match, e.g. 1.0.0+1 (release_version mode)")
	patchPushIOSCmd.Flags().String("flutter-version", "", "Flutter version guard (optional)")
	patchPushIOSCmd.Flags().String("channel", "stable", "Release channel")
	patchPushIOSCmd.Flags().Int("rollout", 100, "Rollout percentage")
	patchPushIOSCmd.Flags().Bool("mandatory", false, "Mark patch mandatory")
	patchPushIOSCmd.Flags().String("notes", "", "Release notes")
	patchPushIOSCmd.Flags().Bool("no-publish", false, "Leave as draft")
	patchPushIOSCmd.Flags().String("build-id", "", "Release build_id, 16 hex chars (build_id mode; from koolbaseBuildId())")
	patchPushIOSCmd.Flags().String("match-mode", "", "Release match mode: build_id (default) or release_version")
	patchCmd.AddCommand(patchPushIOSCmd)
}
