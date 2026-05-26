package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

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
	Example: `  koolbase secrets set STRIPE_KEY --value sk_live_xxx --project proj_123
  echo "$STRIPE_KEY" | koolbase secrets set STRIPE_KEY --stdin --project proj_123
  koolbase secrets set STRIPE_KEY --from-file ./stripe.key --project proj_123`,
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
		value, err := resolveSecretValue(cmd)
		if err != nil {
			return err
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

// resolveSecretValue resolves the secret value from exactly one of --value,
// --stdin, or --from-file. It errors if zero or more than one source is given.
// A single trailing newline is trimmed from stdin and file input so
// `echo "$KEY" | ...` and editor-saved files don't append a stray newline;
// inline --value is taken verbatim.
func resolveSecretValue(cmd *cobra.Command) (string, error) {
	inline, _ := cmd.Flags().GetString("value")
	useStdin, _ := cmd.Flags().GetBool("stdin")
	fromFile, _ := cmd.Flags().GetString("from-file")

	valueSet := cmd.Flags().Changed("value")
	fileSet := cmd.Flags().Changed("from-file")

	sources := 0
	if valueSet {
		sources++
	}
	if useStdin {
		sources++
	}
	if fileSet {
		sources++
	}

	switch {
	case sources == 0:
		return "", fmt.Errorf("provide the secret value with one of --value, --stdin, or --from-file")
	case sources > 1:
		return "", fmt.Errorf("only one of --value, --stdin, or --from-file may be used")
	}

	var value string
	switch {
	case valueSet:
		value = inline
	case useStdin:
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading secret from stdin: %w", err)
		}
		value = trimTrailingNewline(string(data))
	case fileSet:
		data, err := os.ReadFile(fromFile)
		if err != nil {
			return "", fmt.Errorf("reading secret from file: %w", err)
		}
		value = trimTrailingNewline(string(data))
	}

	if value == "" {
		return "", fmt.Errorf("secret value is empty")
	}
	return value, nil
}

// trimTrailingNewline removes a single trailing newline (\n or \r\n).
func trimTrailingNewline(s string) string {
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	return s
}

func init() {
	secretsListCmd.Flags().StringP("project", "p", "", "Project ID")
	secretsSetCmd.Flags().StringP("project", "p", "", "Project ID")
	secretsSetCmd.Flags().String("value", "", "Secret value (inline; avoid for sensitive values — visible in shell history)")
	secretsSetCmd.Flags().Bool("stdin", false, "Read the secret value from stdin")
	secretsSetCmd.Flags().String("from-file", "", "Read the secret value from a file")
	secretsRmCmd.Flags().StringP("project", "p", "", "Project ID")

	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsRmCmd)
}
