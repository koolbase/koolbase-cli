package cmd

import (
	"fmt"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var triggersReplayCmd = &cobra.Command{
	Use:   "replay <trigger_id>",
	Short: "Run a db trigger for the records already in its collection",
	Long: "Queues the trigger's function once for each record already in its collection,\n" +
		"oldest first, as if each had just been created or updated. Use it for data\n" +
		"seeded before the trigger was bound. Only db.record.created and\n" +
		"db.record.updated triggers can be replayed.\n\n" +
		"Deliveries go through the retry queue, paced so they never crowd out real\n" +
		"events. Each carries payload.replay = true and a payload.event_id that is\n" +
		"stable per trigger and record, so a function can recognise a repeat.\n\n" +
		"Run with --dry-run first: it only counts.",
	Example: `  koolbase triggers replay trg_abc123 --dry-run
  koolbase triggers replay trg_abc123 --limit 500`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			if cfg.ProjectID == "" {
				return fmt.Errorf("--project is required")
			}
			projectID = cfg.ProjectID
		}
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		limit, _ := cmd.Flags().GetInt("limit")

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		res, err := client.ReplayTrigger(projectID, args[0], dryRun, limit)
		if err != nil {
			return err
		}
		fmt.Print(formatReplayResult(res))
		return nil
	},
}

// formatReplayResult says what was (or would be) queued, how long it takes,
// and what a repeat run means.
func formatReplayResult(r *api.ReplayResult) string {
	var b strings.Builder
	verb := "Queued"
	if r.DryRun {
		verb = "Dry run: would queue"
	}
	fmt.Fprintf(&b, "%s %d of %d records in %s for %s (%s).\n",
		verb, r.Queued, r.Matching, r.Collection, r.FunctionName, r.EventType)
	if r.Queued > 0 {
		fmt.Fprintf(&b, "About %ds to deliver, paced through the retry queue.\n", r.EstimatedSeconds)
	}
	if r.Remaining > 0 {
		fmt.Fprintf(&b, "%d more records were beyond --limit and not queued. Running again queues the earlier ones again too, with the same event_id.\n", r.Remaining)
	}
	if !r.DryRun && r.Queued > 0 {
		b.WriteString("Each delivery carries payload.replay = true and a stable payload.event_id.\n")
	}
	return b.String()
}

func init() {
	triggersReplayCmd.Flags().StringP("project", "p", "", "Project ID")
	triggersReplayCmd.Flags().Bool("dry-run", false, "Only count what would be queued")
	triggersReplayCmd.Flags().Int("limit", 0, "Queue at most this many records (server default 1000, max 5000)")
	triggersCmd.AddCommand(triggersReplayCmd)
}
