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

const defaultManifest = "koolbase.json"

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Manage your project's backend definition as code",
	Long: `Treat your backend as code.

'koolbase snapshot pull' exports a project's structural definition — collections,
access rules, and per-environment flags / config / version policy — into a single
versioned file (koolbase.json) that you commit to git.

'koolbase snapshot apply' reconciles a target project to that file, idempotently.
Use --dry-run to preview the diff, and run it in CI to keep projects in sync from
one reviewed source of truth.

Secrets, OAuth/SMS credentials, and records are never included.`,
}

var (
	snapshotProject string
	snapshotOutput  string
	snapshotFile    string
	snapshotDryRun  bool
)

var snapshotPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Export a project's backend definition to a file you commit (default: koolbase.json)",
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

		// pretty-print so committed definitions diff cleanly in version control
		var pretty bytes.Buffer
		if json.Indent(&pretty, data, "", "  ") == nil {
			data = pretty.Bytes()
		}

		// "-" means stdout; empty means the default manifest file
		if snapshotOutput == "-" {
			fmt.Println(string(data))
			return nil
		}
		out := snapshotOutput
		if out == "" {
			out = defaultManifest
		}
		if err := os.WriteFile(out, data, 0600); err != nil {
			return fmt.Errorf("failed to write %s: %w", out, err)
		}
		fmt.Printf("Backend definition written to %s\n", out)
		return nil
	},
}

type resourceDiff struct {
	Added     []string `json:"added"`
	Changed   []string `json:"changed"`
	Unchanged []string `json:"unchanged"`
}

type envDiff struct {
	Slug            string       `json:"slug"`
	Name            string       `json:"name"`
	Matched         bool         `json:"matched"`
	Flags           resourceDiff `json:"flags"`
	Configs         resourceDiff `json:"configs"`
	VersionPolicies resourceDiff `json:"version_policies"`
}

type applyResult struct {
	Applied             int      `json:"applied"`
	DryRun              bool     `json:"dry_run"`
	TargetProjectID     string   `json:"target_project_id"`
	SkippedEnvironments []string `json:"skipped_environments"`
	Diff                struct {
		Added []struct {
			Name string `json:"name"`
		} `json:"added"`
		Changed []struct {
			Name string `json:"name"`
		} `json:"changed"`
		Unchanged    []string  `json:"unchanged"`
		Removed      []string  `json:"removed"`
		Environments []envDiff `json:"environments"`
	} `json:"diff"`
}

func printResourceDiff(label string, rd resourceDiff) {
	for _, k := range rd.Added {
		fmt.Printf("    + %s (%s)\n", k, label)
	}
	for _, k := range rd.Changed {
		fmt.Printf("    ~ %s (%s)\n", k, label)
	}
}

var snapshotApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Reconcile a target project to your backend definition (use --dry-run to preview)",
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
		fmt.Printf("%s project %s\n\n", mode, res.TargetProjectID)

		fmt.Printf("Collections: %d added · %d changed · %d unchanged\n",
			len(res.Diff.Added), len(res.Diff.Changed), len(res.Diff.Unchanged))
		for _, a := range res.Diff.Added {
			fmt.Printf("  + %s\n", a.Name)
		}
		for _, ch := range res.Diff.Changed {
			fmt.Printf("  ~ %s\n", ch.Name)
		}
		if len(res.Diff.Removed) > 0 {
			fmt.Printf("  (in target only, left untouched: %v)\n", res.Diff.Removed)
		}

		if len(res.Diff.Environments) > 0 {
			fmt.Println("\nEnvironments:")
			anySkipped := false
			for _, env := range res.Diff.Environments {
				if !env.Matched {
					fmt.Printf("  %s — no matching env in target (skipped)\n", env.Slug)
					anySkipped = true
					continue
				}
				changes := len(env.Flags.Added) + len(env.Flags.Changed) +
					len(env.Configs.Added) + len(env.Configs.Changed) +
					len(env.VersionPolicies.Added) + len(env.VersionPolicies.Changed)
				if changes == 0 {
					fmt.Printf("  %s — in sync\n", env.Slug)
					continue
				}
				fmt.Printf("  %s:\n", env.Slug)
				printResourceDiff("flags", env.Flags)
				printResourceDiff("configs", env.Configs)
				printResourceDiff("version policies", env.VersionPolicies)
			}
			if anySkipped {
				fmt.Println("  → create matching environments (by slug) in the target to promote their config.")
			}
		}

		if !res.DryRun {
			fmt.Printf("\n%d resource(s) applied.\n", res.Applied)
		}
		return nil
	},
}

func init() {
	snapshotPullCmd.Flags().StringVar(&snapshotProject, "project", "", "project ID to export from (required)")
	snapshotPullCmd.Flags().StringVarP(&snapshotOutput, "output", "o", "", "output file (default: koolbase.json; use '-' for stdout)")
	snapshotPullCmd.MarkFlagRequired("project")

	snapshotApplyCmd.Flags().StringVar(&snapshotProject, "project", "", "target project ID to apply to (required)")
	snapshotApplyCmd.Flags().StringVarP(&snapshotFile, "file", "f", defaultManifest, "backend definition file to apply (default: koolbase.json)")
	snapshotApplyCmd.Flags().BoolVar(&snapshotDryRun, "dry-run", false, "preview the diff without writing")
	snapshotApplyCmd.MarkFlagRequired("project")

	snapshotCmd.AddCommand(snapshotPullCmd, snapshotApplyCmd)
}
