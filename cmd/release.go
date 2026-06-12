package cmd

import (
	"encoding/hex"
	"fmt"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var releaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Manage VM-level code-push releases (registered app binaries)",
}

var releaseCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Register a built App binary as a release (keyed by build_id)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		appID, _ := cmd.Flags().GetString("app")
		binary, _ := cmd.Flags().GetString("binary")
		channel, _ := cmd.Flags().GetString("channel")
		platform, _ := cmd.Flags().GetString("platform")
		flutterVersion, _ := cmd.Flags().GetString("flutter-version")
		appVersion, _ := cmd.Flags().GetString("app-version")

		if appID == "" {
			return fmt.Errorf("--app is required")
		}
		if binary == "" {
			return fmt.Errorf("--binary is required (path to the built App binary)")
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
		fmt.Printf("\n  Release registered → %s\n", rel.ID)
		fmt.Printf("  build_id: %s  platform: %s  channel: %s\n", rel.BuildID, rel.Platform, rel.Channel)
		return nil
	},
}

var releaseListCmd = &cobra.Command{
	Use:   "list",
	Short: "List releases for an app",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		appID, _ := cmd.Flags().GetString("app")
		if appID == "" {
			return fmt.Errorf("--app is required")
		}
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		releases, err := client.ListReleases(appID)
		if err != nil {
			return err
		}
		if len(releases) == 0 {
			fmt.Println("No releases found")
			return nil
		}
		fmt.Printf("\n  %-30s %-18s %-8s %-10s %-10s %s\n",
			"ID", "BUILD_ID", "PLATFORM", "CHANNEL", "FLUTTER", "CREATED")
		for _, r := range releases {
			fmt.Printf("  %-30s %-18s %-8s %-10s %-10s %s\n",
				r.ID, r.BuildID, r.Platform, r.Channel, r.FlutterVersion, formatTime(r.CreatedAt))
		}
		fmt.Println()
		return nil
	},
}

func init() {
	releaseCreateCmd.Flags().String("app", "", "App (project) ID (required)")
	releaseCreateCmd.Flags().String("binary", "", "Path to the built App binary (required)")
	releaseCreateCmd.Flags().String("channel", "stable", "Release channel")
	releaseCreateCmd.Flags().String("platform", "macos", "Platform (ios, android, macos)")
	releaseCreateCmd.Flags().String("flutter-version", "", "Flutter version this binary was built with")
	releaseCreateCmd.Flags().String("app-version", "", "App version string")

	releaseListCmd.Flags().String("app", "", "App (project) ID (required)")

	releaseCmd.AddCommand(releaseCreateCmd)
	releaseCmd.AddCommand(releaseListCmd)
	rootCmd.AddCommand(releaseCmd)
}
