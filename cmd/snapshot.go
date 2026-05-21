package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Export and promote project config (collections, access rules) between projects",
}

var (
	snapshotProject string
	snapshotOutput  string
	snapshotFile    string
	snapshotDryRun  bool
)

var snapshotPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Export a project's config snapshot to a file (or stdout)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)

		data, err := client.SnapshotPull(snapshotProject)
		if err != nil {
			return err
		}

		// pretty-print so saved snapshots diff cleanly in version control
		var pretty bytes.Buffer
		if json.Indent(&pretty, data, "", "  ") == nil {
			data = pretty.Bytes()
		}

		if snapshotOutput == "" || snapshotOutput == "-" {
			fmt.Println(string(data))
			return nil
		}
		if err := os.WriteFile(snapshotOutput, data, 0600); err != nil {
			return fmt.Errorf("failed to write %s: %w", snapshotOutput, err)
		}
		fmt.Printf("Snapshot written to %s\n", snapshotOutput)
		return nil
	},
}

type applyResult struct {
	Applied         int    `json:"applied"`
	DryRun          bool   `json:"dry_run"`
	TargetProjectID string `json:"target_project_id"`
	Diff            struct {
		Added     []struct {
			Name string `json:"name"`
		} `json:"added"`
		Changed []struct {
			Name string `json:"name"`
		} `json:"changed"`
		Unchanged []string `json:"unchanged"`
		Removed   []string `json:"removed"`
	} `json:"diff"`
}

var snapshotApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a config snapshot to a project (use --dry-run to preview)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		raw, err := os.ReadFile(snapshotFile)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", snapshotFile, err)
		}

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		data, err := client.SnapshotApply(snapshotProject, json.RawMessage(raw), snapshotDryRun)
		if err != nil {
			return err
		}

		var res applyResult
		if err := json.Unmarshal(data, &res); err != nil {
			fmt.Println(string(data)) // fall back to raw if shape changes
			return nil
		}

		mode := "Applied to"
		if res.DryRun {
			mode = "Dry run against"
		}
		fmt.Printf("%s project %s\n", mode, res.TargetProjectID)
		fmt.Printf("  added:     %d\n", len(res.Diff.Added))
		fmt.Printf("  changed:   %d\n", len(res.Diff.Changed))
		fmt.Printf("  unchanged: %d\n", len(res.Diff.Unchanged))
		if len(res.Diff.Removed) > 0 {
			fmt.Printf("  removed (reported, not deleted): %v\n", res.Diff.Removed)
		}
		for _, a := range res.Diff.Added {
			fmt.Printf("    + %s\n", a.Name)
		}
		for _, ch := range res.Diff.Changed {
			fmt.Printf("    ~ %s\n", ch.Name)
		}
		if !res.DryRun {
			fmt.Printf("\n%d collection(s) applied.\n", res.Applied)
		}
		return nil
	},
}

func init() {
	snapshotPullCmd.Flags().StringVar(&snapshotProject, "project", "", "project ID to export from (required)")
	snapshotPullCmd.Flags().StringVarP(&snapshotOutput, "output", "o", "", "output file (default: stdout)")
	snapshotPullCmd.MarkFlagRequired("project")

	snapshotApplyCmd.Flags().StringVar(&snapshotProject, "project", "", "target project ID to apply to (required)")
	snapshotApplyCmd.Flags().StringVarP(&snapshotFile, "file", "f", "", "snapshot file to apply (required)")
	snapshotApplyCmd.Flags().BoolVar(&snapshotDryRun, "dry-run", false, "preview the diff without writing")
	snapshotApplyCmd.MarkFlagRequired("project")
	snapshotApplyCmd.MarkFlagRequired("file")

	snapshotCmd.AddCommand(snapshotPullCmd, snapshotApplyCmd)
}
