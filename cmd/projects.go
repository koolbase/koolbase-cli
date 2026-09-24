package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Work with your projects",
}

var projectsListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the projects in your organization, with their IDs",
	Long: "Lists the projects in the organization you are signed in to, so you can\n" +
		"find a project ID without opening the dashboard. Use --org to list another\n" +
		"organization you belong to.",
	Example: `  koolbase projects list
  koolbase projects list --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)

		// Same as koolbase create and the MCP server: the organization comes
		// from the signed-in account unless --org says otherwise.
		orgID, _ := cmd.Flags().GetString("org")
		if orgID == "" {
			who, err := client.Whoami()
			if err != nil {
				return err
			}
			orgID = who.OrgID
		}

		projects, err := client.ListProjects(orgID)
		if err != nil {
			return err
		}

		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			out, _ := json.MarshalIndent(projects, "", "  ")
			fmt.Println(string(out))
			return nil
		}
		fmt.Print(formatProjects(projects, cfg.ProjectID))
		return nil
	},
}

// formatProjects renders the project table. The saved default project, if
// any, is marked with *.
func formatProjects(projects []api.Project, current string) string {
	if len(projects) == 0 {
		return "No projects in this organization yet. Create one in the dashboard or with: koolbase create\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  %-38s %s\n", "ID", "NAME")
	fmt.Fprintf(&b, "  %-38s %s\n", "--", "----")
	for _, p := range projects {
		mark := " "
		if current != "" && p.ID == current {
			mark = "*"
		}
		fmt.Fprintf(&b, "%s %-38s %s\n", mark, p.ID, p.Name)
	}
	if current != "" {
		b.WriteString("\n* your default project\n")
	}
	b.WriteString("\nUse an ID with --project <id>, for example: koolbase functions list --project <id>\n")
	return b.String()
}

func init() {
	projectsListCmd.Flags().String("org", "", "Organization ID (defaults to the one you are signed in to)")
	projectsListCmd.Flags().Bool("json", false, "Print the projects as JSON")
	projectsCmd.AddCommand(projectsListCmd)
	rootCmd.AddCommand(projectsCmd)
}
