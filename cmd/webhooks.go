package cmd

import (
	"fmt"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

const webhooksDocs = "https://docs.koolbase.com/database/webhooks"

var webhooksCmd = &cobra.Command{
	Use:   "webhooks",
	Short: "Send database record events to your own server or another service",
	Long: "Webhooks POST record events (created, updated, deleted) from a collection to an HTTPS URL,\n" +
		"signed so the receiver can verify them, with retries. Owners and admins only.\n\n" +
		"Docs: " + webhooksDocs,
}

// webhookClient loads the config and resolves the project the same way the
// other commands do: --project, else the saved default.
func webhookClient(cmd *cobra.Command) (*api.Client, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", err
	}
	projectID, _ := cmd.Flags().GetString("project")
	if projectID == "" {
		if cfg.ProjectID == "" {
			return nil, "", fmt.Errorf("--project is required")
		}
		projectID = cfg.ProjectID
	}
	return api.NewClient(cfg.BaseURL, cfg.APIKey), projectID, nil
}

// resolveWebhookEvents accepts repeated --event flags and comma lists, and the
// insert/update/delete shorthands, like triggers create.
func resolveWebhookEvents(raw []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range raw {
		for _, e := range strings.Split(r, ",") {
			if e = resolveTriggerEvent(e); e != "" && !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	return out
}

func formatWebhooks(list []api.Webhook) string {
	if len(list) == 0 {
		return "No webhooks.\nCreate one with: koolbase webhooks create --url <https url> --collection <name> --event insert\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-38s %-18s %-8s %s\n", "ID", "COLLECTION", "ENABLED", "EVENTS -> URL")
	for _, w := range list {
		enabled := "no"
		if w.Enabled {
			enabled = "yes"
		}
		fmt.Fprintf(&b, "%-38s %-18s %-8s %s -> %s\n", w.ID, w.Collection, enabled, strings.Join(w.Events, ","), w.URL)
	}
	return b.String()
}

func formatCreatedWebhook(c *api.CreatedWebhook) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Webhook created: %s\n", c.Webhook.ID)
	fmt.Fprintf(&b, "  %s on %s -> %s\n\n", strings.Join(c.Webhook.Events, ","), c.Webhook.Collection, c.Webhook.URL)
	b.WriteString("Signing secret (shown once, store it now):\n")
	fmt.Fprintf(&b, "  %s\n\n", c.Secret)
	fmt.Fprintf(&b, "Use it to verify the X-Koolbase-Signature header: %s\n", webhooksDocs)
	return b.String()
}

func formatDeliveries(list []api.WebhookDelivery) string {
	if len(list) == 0 {
		return "No deliveries yet.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-38s %-18s %-10s %-8s %-5s %s\n", "DELIVERY", "EVENT", "STATUS", "ATTEMPT", "CODE", "ERROR")
	for _, d := range list {
		code := "-"
		if d.LastStatusCode != nil {
			code = fmt.Sprint(*d.LastStatusCode)
		}
		errMsg := ""
		if d.LastError != nil {
			errMsg = *d.LastError
		}
		fmt.Fprintf(&b, "%-38s %-18s %-10s %-8s %-5s %s\n", d.ID, d.EventType, d.Status,
			fmt.Sprintf("%d/%d", d.Attempt, d.MaxAttempts), code, errMsg)
	}
	return b.String()
}

var webhooksListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the project's webhooks",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := webhookClient(cmd)
		if err != nil {
			return err
		}
		list, err := client.ListWebhooks(projectID)
		if err != nil {
			return err
		}
		fmt.Print(formatWebhooks(list))
		return nil
	},
}

var webhooksCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a webhook (the signing secret is shown once)",
	Example: `  koolbase webhooks create --url https://example.com/hook --collection orders --event insert
  koolbase webhooks create --url https://example.com/hook --collection orders --event insert,update,delete`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := webhookClient(cmd)
		if err != nil {
			return err
		}
		url, _ := cmd.Flags().GetString("url")
		collection, _ := cmd.Flags().GetString("collection")
		raw, _ := cmd.Flags().GetStringArray("event")
		if url == "" || collection == "" || len(raw) == 0 {
			return fmt.Errorf("--url, --collection and at least one --event are required")
		}
		created, err := client.CreateWebhook(projectID, url, collection, resolveWebhookEvents(raw))
		if err != nil {
			return err
		}
		fmt.Print(formatCreatedWebhook(created))
		return nil
	},
}

var webhooksDeleteCmd = &cobra.Command{
	Use:     "delete <webhook-id>",
	Aliases: []string{"rm"},
	Short:   "Delete a webhook and its delivery log",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := webhookClient(cmd)
		if err != nil {
			return err
		}
		if err := client.DeleteWebhook(projectID, args[0]); err != nil {
			return err
		}
		fmt.Printf("Webhook %s deleted\n", args[0])
		return nil
	},
}

var webhooksDeliveriesCmd = &cobra.Command{
	Use:   "deliveries <webhook-id>",
	Short: "Show a webhook's recent deliveries",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := webhookClient(cmd)
		if err != nil {
			return err
		}
		limit, _ := cmd.Flags().GetInt("limit")
		list, err := client.ListWebhookDeliveries(projectID, args[0], limit)
		if err != nil {
			return err
		}
		fmt.Print(formatDeliveries(list))
		return nil
	},
}

var webhooksRedeliverCmd = &cobra.Command{
	Use:   "redeliver <delivery-id>",
	Short: "Send a delivery again, from the start",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := webhookClient(cmd)
		if err != nil {
			return err
		}
		if err := client.RedeliverWebhook(projectID, args[0]); err != nil {
			return err
		}
		fmt.Printf("Delivery %s queued again\n", args[0])
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{webhooksListCmd, webhooksCreateCmd, webhooksDeleteCmd, webhooksDeliveriesCmd, webhooksRedeliverCmd} {
		c.Flags().StringP("project", "p", "", "Project ID")
		webhooksCmd.AddCommand(c)
	}
	webhooksCreateCmd.Flags().String("url", "", "HTTPS URL to send events to (port 443 or 8443) (required)")
	webhooksCreateCmd.Flags().String("collection", "", "Collection to watch (required)")
	webhooksCreateCmd.Flags().StringArray("event", nil, "db.record.created|updated|deleted, or insert|update|delete; repeat or comma-separate (required)")
	webhooksDeliveriesCmd.Flags().Int("limit", 20, "How many deliveries to show (max 100)")
	rootCmd.AddCommand(webhooksCmd)
}
