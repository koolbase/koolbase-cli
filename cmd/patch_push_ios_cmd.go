package cmd

import (
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
)

// patch push-ios: publish a pre-built iOS KBC bytecode container.
//
// Unlike `patch push` (Android/macOS whole-blob binary patches, which analyze
// an ELF base and extract snapshots), the iOS artifact is a KBC container
// produced by `koolbase patch ios`. The server treats artifacts as opaque
// bytes, so this command is pure publish plumbing:
//
//	CreateRelease (or --release) -> CreatePatch(draft, checksum inline)
//	-> UploadPatchArtifact -> PublishPatch (unless --no-publish).
//
// Rung 3 matching model: release_version (app_version). iOS build_id
// stamping is deferred to hardening.
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

		if appID == "" {
			return fmt.Errorf("--app is required")
		}
		if container == "" {
			return fmt.Errorf("--container is required (the .kbc built by `koolbase patch ios`)")
		}

		blob, err := os.ReadFile(container)
		if err != nil {
			return fmt.Errorf("could not read container: %w", err)
		}
		checksum := fmt.Sprintf("sha256:%x", sha256.Sum256(blob))
		fmt.Printf("  container %s (%d bytes)\n  checksum %s\n", container, len(blob), checksum)

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)

		// Release: use existing, or create a release_version-matched one.
		if releaseID == "" {
			if appVersion == "" {
				return fmt.Errorf("--app-version is required when --release is not given (release_version matching)")
			}
			rel, rerr := client.CreateRelease(appID, api.CreateReleaseRequest{
				BuildID:        buildID,
				Platform:       "ios",
				AppVersion:     appVersion,
				FlutterVersion: flutterVersion,
				MatchMode:      "release_version",
				Channel:        channel,
			})
			if rerr != nil {
				return fmt.Errorf("create release failed: %w", rerr)
			}
			releaseID = rel.ID
			fmt.Printf("  ✓ release created %s (ios %s, channel %s)\n", releaseID, appVersion, channel)
		}

		patch, err := client.CreatePatch(appID, releaseID, api.CreatePatchRequest{
			ReleaseID:         releaseID,
			RolloutPercentage: rollout,
			Mandatory:         mandatory,
			ReleaseNotes:      notes,
			Checksum:          checksum,
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
	patchPushIOSCmd.Flags().String("container", "", "Path to the .kbc container")
	patchPushIOSCmd.Flags().String("release", "", "Existing release ID (skips release creation)")
	patchPushIOSCmd.Flags().String("app-version", "", "Release version to match, e.g. 1.0.0+1")
	patchPushIOSCmd.Flags().String("flutter-version", "", "Flutter version guard (optional)")
	patchPushIOSCmd.Flags().String("channel", "stable", "Release channel")
	patchPushIOSCmd.Flags().Int("rollout", 100, "Rollout percentage")
	patchPushIOSCmd.Flags().Bool("mandatory", false, "Mark patch mandatory")
	patchPushIOSCmd.Flags().String("notes", "", "Release notes")
	patchPushIOSCmd.Flags().Bool("no-publish", false, "Leave as draft")
	patchPushIOSCmd.Flags().String("build-id", "", "Release build_id (iOS content-hash stamping TBD; synthetic OK for release_version matching)")
	patchCmd.AddCommand(patchPushIOSCmd)
}
