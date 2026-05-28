package cmd

import (
	"fmt"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var uniqueConstraintsCmd = &cobra.Command{
	Use:     "unique-constraints",
	Aliases: []string{"constraints", "uc"},
	Short:   "Manage unique constraints on a collection",
}

var ucListCmd = &cobra.Command{
	Use:     "list <collection>",
	Short:   "List unique constraints declared on a collection",
	Example: `  koolbase unique-constraints list users --project proj_123`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		collection := args[0]
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
		constraints, err := client.ListUniqueConstraints(projectID, collection)
		if err != nil {
			return err
		}

		if len(constraints) == 0 {
			fmt.Printf("No unique constraints on %q.\n", collection)
			fmt.Printf("Declare one with: koolbase unique-constraints declare %s --fields email --project <id>\n", collection)
			return nil
		}

		fmt.Printf("%-36s %-30s %s\n", "ID", "FIELDS", "CASE-INSENSITIVE")
		fmt.Printf("%-36s %-30s %s\n", "--", "------", "----------------")
		for _, uc := range constraints {
			ci := "no"
			if uc.CaseInsensitive {
				ci = "yes"
			}
			fmt.Printf("%-36s %-30s %s\n", uc.ID, strings.Join(uc.Fields, ", "), ci)
		}
		return nil
	},
}

var ucDeclareCmd = &cobra.Command{
	Use:   "declare <collection>",
	Short: "Declare a unique constraint on a collection",
	Long: `Declare a unique constraint over one or more fields of a collection.

A single field enforces uniqueness on that field; multiple fields form a
composite constraint (the combination must be unique). Use --case-insensitive
to fold case (so "Bob@x.com" and "bob@x.com" collide).

If the collection already holds values that would violate the constraint, the
command reports the offending groups and does not create it — dedupe those
records first, then re-run.`,
	Example: `  koolbase unique-constraints declare users --fields email --case-insensitive --project proj_123
  koolbase unique-constraints declare memberships --fields user_id,org_id --project proj_123`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		collection := args[0]
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

		fields, _ := cmd.Flags().GetStringSlice("fields")
		if len(fields) == 0 {
			return fmt.Errorf("--fields is required (e.g. --fields email, or --fields user_id,org_id for a composite)")
		}
		caseInsensitive, _ := cmd.Flags().GetBool("case-insensitive")

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		uc, dupes, err := client.CreateUniqueConstraint(projectID, collection, fields, caseInsensitive)
		if err != nil {
			if len(dupes) > 0 {
				fmt.Printf("Cannot declare constraint — %q already has duplicate values for %s:\n\n",
					collection, strings.Join(fields, ", "))
				for _, g := range dupes {
					fmt.Printf("   %-40s (%d records)\n", strings.Join(g.Values, ", "), g.Count)
				}
				fmt.Println("\nDedupe these records, then declare the constraint again.")
				return fmt.Errorf("duplicate values present")
			}
			return err
		}

		ci := "no"
		if uc.CaseInsensitive {
			ci = "yes"
		}
		fmt.Printf("\nUnique constraint declared\n")
		fmt.Printf("   ID:               %s\n", uc.ID)
		fmt.Printf("   Collection:       %s\n", collection)
		fmt.Printf("   Fields:           %s\n", strings.Join(uc.Fields, ", "))
		fmt.Printf("   Case-insensitive: %s\n", ci)
		return nil
	},
}

var ucDeleteCmd = &cobra.Command{
	Use:     "delete <collection> <constraint-id>",
	Short:   "Delete a unique constraint (drops its backing index)",
	Example: `  koolbase unique-constraints delete users 4f1c8e2a-... --project proj_123`,
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		collection := args[0]
		constraintID := args[1]
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
		if err := client.DeleteUniqueConstraint(projectID, collection, constraintID); err != nil {
			return err
		}
		fmt.Printf("Unique constraint %s deleted\n", constraintID)
		return nil
	},
}

func init() {
	ucListCmd.Flags().StringP("project", "p", "", "Project ID")
	ucDeclareCmd.Flags().StringP("project", "p", "", "Project ID")
	ucDeclareCmd.Flags().StringSlice("fields", nil, "Field(s) the constraint covers (comma-separated for composite, e.g. user_id,org_id)")
	ucDeclareCmd.Flags().Bool("case-insensitive", false, "Fold case when enforcing uniqueness")
	ucDeleteCmd.Flags().StringP("project", "p", "", "Project ID")

	uniqueConstraintsCmd.AddCommand(ucListCmd)
	uniqueConstraintsCmd.AddCommand(ucDeclareCmd)
	uniqueConstraintsCmd.AddCommand(ucDeleteCmd)
}
