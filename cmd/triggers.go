package cmd

import (
	"fmt"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var triggersCmd = &cobra.Command{
	Use:     "triggers",
	Aliases: []string{"trigger"},
	Short:   "Manage database event triggers for your functions",
}

var triggersListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List database triggers in a project",
	Example: `  koolbase triggers list --project proj_123`,
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

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		triggers, err := client.ListTriggers(projectID)
		if err != nil {
			return err
		}
		if len(triggers) == 0 {
			fmt.Println("No triggers found.")
			fmt.Println("Create one with: koolbase triggers create --function <name> --event <event, e.g. db.record.created> [--collection <collection or bucket>]")
			return nil
		}

		fmt.Printf("%-38s %-20s %-24s %-16s %-8s %s\n",
			"ID", "FUNCTION", "EVENT", "COLLECTION", "ENABLED", "CREATED")
		fmt.Printf("%-38s %-20s %-24s %-16s %-8s %s\n",
			"--", "--------", "-----", "----------", "-------", "-------")
		for _, t := range triggers {
			collection := t.Collection
			if collection == "" {
				collection = "—"
			}
			enabled := "no"
			if t.Enabled {
				enabled = "yes"
			}
			fmt.Printf("%-38s %-20s %-24s %-16s %-8s %s\n",
				t.ID, t.FunctionName, t.EventType, collection, enabled, formatTime(t.CreatedAt))
		}
		return nil
	},
}

var triggersCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an event trigger (database, storage or auth)",
	Long: "Binds a function to an event. The server validates the event and lists every supported one if it does not recognise it.\n\n" +
		"Common events: db.record.created, db.record.updated, db.record.deleted, storage.object.created, auth.user.registered.\n" +
		"insert, update and delete are accepted as short names for the db.record.* events.\n\n" +
		"db.* and storage.* events need --collection (the collection, or the bucket for storage). auth.* events are project-wide.",
	Example: `  koolbase triggers create --function notify --event db.record.created --collection orders
  koolbase triggers create --function resize --event storage.object.created --collection avatars
  koolbase triggers create --function welcome --event auth.user.registered`,
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
		functionName, _ := cmd.Flags().GetString("function")
		eventType, _ := cmd.Flags().GetString("event")
		collection, _ := cmd.Flags().GetString("collection")
		if functionName == "" {
			return fmt.Errorf("--function is required")
		}
		if eventType == "" {
			return fmt.Errorf("--event is required")
		}
		eventType = resolveTriggerEvent(eventType)
		if triggerNeedsTarget(eventType) && collection == "" {
			return fmt.Errorf("--collection is required for %s (the collection for db.* events, the bucket for storage.* events)", eventType)
		}

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		trigger, err := client.CreateTrigger(projectID, functionName, eventType, collection)
		if err != nil {
			return err
		}
		if trigger != nil && trigger.ID != "" {
			fmt.Printf("Trigger created: %s\n", trigger.ID)
			if triggerNeedsTarget(eventType) {
				fmt.Printf("  %s fires on %s in %s\n", functionName, eventType, collection)
			} else {
				fmt.Printf("  %s fires on %s\n", functionName, eventType)
			}
		} else {
			fmt.Println("Trigger created.")
		}
		return nil
	},
}

var triggersDeleteCmd = &cobra.Command{
	Use:     "delete <trigger_id>",
	Aliases: []string{"rm"},
	Short:   "Delete a database trigger",
	Example: `  koolbase triggers delete trg_abc123 --project proj_123`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		triggerID := args[0]
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
		if err := client.DeleteTrigger(projectID, triggerID); err != nil {
			return err
		}
		fmt.Printf("Trigger %s deleted\n", triggerID)
		return nil
	},
}

var triggersStatsCmd = &cobra.Command{
	Use:     "stats",
	Short:   "Show execution stats per trigger",
	Example: `  koolbase triggers stats --project proj_123`,
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
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		stats, err := client.GetTriggerStats(projectID)
		if err != nil {
			return err
		}
		if len(stats) == 0 {
			fmt.Println("No trigger activity yet.")
			return nil
		}
		fmt.Printf("%-20s %-10s %-16s %-7s %-7s %-7s %-8s %s\n",
			"FUNCTION", "EVENT", "COLLECTION", "TOTAL", "OK", "FAIL", "TIMEOUT", "SUCCESS%")
		fmt.Printf("%-20s %-10s %-16s %-7s %-7s %-7s %-8s %s\n",
			"--------", "-----", "----------", "-----", "--", "----", "-------", "--------")
		for _, s := range stats {
			collection := s.Collection
			if collection == "" {
				collection = "—"
			}
			pct := "—"
			if s.Total > 0 {
				pct = fmt.Sprintf("%.1f%%", float64(s.Successes)/float64(s.Total)*100)
			}
			fmt.Printf("%-20s %-10s %-16s %-7d %-7d %-7d %-8d %s\n",
				s.FunctionName, s.EventType, collection, s.Total, s.Successes, s.Failures, s.Timeouts, pct)
		}
		return nil
	},
}

func init() {
	triggersListCmd.Flags().StringP("project", "p", "", "Project ID")

	triggersCreateCmd.Flags().StringP("project", "p", "", "Project ID")
	triggersCreateCmd.Flags().String("function", "", "Function name to invoke (required)")
	triggersCreateCmd.Flags().String("event", "", "Event to fire on, e.g. db.record.created, storage.object.created, auth.user.registered; insert/update/delete also work for db.record.* (required)")
	triggersCreateCmd.Flags().String("collection", "", "Collection (db.* events) or bucket (storage.* events) to watch; not used for auth.* events")

	triggersDeleteCmd.Flags().StringP("project", "p", "", "Project ID")

	triggersStatsCmd.Flags().StringP("project", "p", "", "Project ID")

	triggersCmd.AddCommand(triggersListCmd)
	triggersCmd.AddCommand(triggersCreateCmd)
	triggersCmd.AddCommand(triggersDeleteCmd)
	triggersCmd.AddCommand(triggersStatsCmd)
}

// triggerEventAliases maps the short names this command's help once taught to
// the event names the server accepts. Anything else passes through unchanged:
// the server validates it and lists every supported event, so the CLI keeps
// no copy of that list to drift out of date.
var triggerEventAliases = map[string]string{
	"insert": "db.record.created",
	"update": "db.record.updated",
	"delete": "db.record.deleted",
}

func resolveTriggerEvent(event string) string {
	e := strings.TrimSpace(event)
	if full, ok := triggerEventAliases[strings.ToLower(e)]; ok {
		return full
	}
	return e
}

// triggerNeedsTarget reports whether an event is bound to a collection (db.*)
// or a bucket (storage.*). auth.* events are project-wide and take no target.
func triggerNeedsTarget(event string) bool {
	return strings.HasPrefix(event, "db.") || strings.HasPrefix(event, "storage.")
}
