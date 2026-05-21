package cmd

import (
	"fmt"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage function secrets (environment variables) for a project",
}

var secretsListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List secret names in a project (values are never shown)",
	Example: `  koolbase secrets list --project proj_123`,
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
		secrets, err := client.ListSecrets(projectID)
		if err != nil {
			return err
		}

		if len(secrets) == 0 {
			fmt.Println("No secrets found.")
			fmt.Println("Add one with: koolbase secrets set <name> --value <value> --project <id>")
			return nil
		}

		fmt.Printf("%-30s %s\n", "NAME", "UPDATED")
		fmt.Printf("%-30s %s\n", "----", "-------")
		for _, s := range secrets {
			updated := "—"
			if s.UpdatedAt != "" && len(s.UpdatedAt) >= 10 {
				updated = s.UpdatedAt[:10]
			} else if s.CreatedAt != "" && len(s.CreatedAt) >= 10 {
				updated = s.CreatedAt[:10]
			}
			fmt.Printf("%-30s %s\n", s.Name, updated)
		}
		return nil
	},
}

var secretsSetCmd = &cobra.Command{
	Use:     "set <name>",
	Short:   "Create or update a secret (upsert)",
	Example: `  koolbase secrets set STRIPE_KEY --value sk_live_xxx --project proj_123`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

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
		value, _ := cmd.Flags().GetString("value")
		if value == "" {
			return fmt.Errorf("--value is required")
		}

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		if err := client.UpsertSecret(projectID, name, value); err != nil {
			return err
		}

		fmt.Printf("Secret %s saved\n", name)
		return nil
	},
}

var secretsRmCmd = &cobra.Command{
	Use:     "rm <name>",
	Aliases: []string{"delete"},
	Short:   "Delete a secret",
	Example: `  koolbase secrets rm STRIPE_KEY --project proj_123`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

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
		if err := client.DeleteSecret(projectID, name); err != nil {
			return err
		}

		fmt.Printf("Secret %s deleted\n", name)
		return nil
	},
}

func init() {
	secretsListCmd.Flags().StringP("project", "p", "", "Project ID")
	secretsSetCmd.Flags().StringP("project", "p", "", "Project ID")
	secretsSetCmd.Flags().String("value", "", "Secret value")
	secretsRmCmd.Flags().StringP("project", "p", "", "Project ID")

	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsRmCmd)
}
