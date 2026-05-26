package cmd

import (
	"fmt"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var dlqCmd = &cobra.Command{
	Use:     "dlq",
	Aliases: []string{"dead-letters"},
	Short:   "Inspect and manage failed function invocations (dead-letter queue)",
}

var dlqListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List dead-lettered function invocations in a project",
	Example: `  koolbase dlq list --project proj_123 --limit 20`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			if cfg.ProjectID != "" {
				projectID = cfg.ProjectID
			} else {
				return fmt.Errorf("--project is required")
			}
		}
		limit, _ := cmd.Flags().GetInt("limit")

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		letters, err := client.ListDeadLetters(projectID, limit)
		if err != nil {
			return err
		}
		if len(letters) == 0 {
			fmt.Println("No dead letters.")
			return nil
		}

		fmt.Printf("%-38s %-20s %-16s %-9s %-17s %s\n",
			"ID", "FUNCTION", "EVENT", "ATTEMPTS", "FAILED", "LAST ERROR")
		fmt.Printf("%-38s %-20s %-16s %-9s %-17s %s\n",
			"--", "--------", "-----", "--------", "------", "----------")
		for _, d := range letters {
			fmt.Printf("%-38s %-20s %-16s %-9d %-17s %s\n",
				d.ID,
				dlqTruncate(d.FunctionName, 20),
				dlqTruncate(d.EventType, 16),
				d.Attempts,
				formatTime(d.FailedAt),
				dlqTruncate(d.LastError, 60),
			)
		}
		return nil
	},
}

var dlqReplayCmd = &cobra.Command{
	Use:     "replay <id>",
	Short:   "Re-enqueue a dead-lettered invocation (removes it from the queue)",
	Example: `  koolbase dlq replay <id> --project proj_123`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			if cfg.ProjectID != "" {
				projectID = cfg.ProjectID
			} else {
				return fmt.Errorf("--project is required")
			}
		}
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		if err := client.ReplayDeadLetter(projectID, id); err != nil {
			return err
		}
		fmt.Printf("Dead letter %s replayed (re-enqueued and removed from the queue)\n", id)
		return nil
	},
}

var dlqRmCmd = &cobra.Command{
	Use:     "rm <id>",
	Aliases: []string{"delete"},
	Short:   "Delete a dead-lettered invocation without replaying it",
	Example: `  koolbase dlq rm <id> --project proj_123`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			if cfg.ProjectID != "" {
				projectID = cfg.ProjectID
			} else {
				return fmt.Errorf("--project is required")
			}
		}
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		if err := client.DeleteDeadLetter(projectID, id); err != nil {
			return err
		}
		fmt.Printf("Dead letter %s deleted\n", id)
		return nil
	},
}

func dlqTruncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func init() {
	dlqListCmd.Flags().StringP("project", "p", "", "Project ID")
	dlqListCmd.Flags().Int("limit", 50, "Max number of dead letters to list")
	dlqReplayCmd.Flags().StringP("project", "p", "", "Project ID")
	dlqRmCmd.Flags().StringP("project", "p", "", "Project ID")

	dlqCmd.AddCommand(dlqListCmd)
	dlqCmd.AddCommand(dlqReplayCmd)
	dlqCmd.AddCommand(dlqRmCmd)
}
