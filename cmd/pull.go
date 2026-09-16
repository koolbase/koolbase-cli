package cmd

import (
	"fmt"
	"os"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

// pull fetches the latest stored export and applies it, which is the
// two-step download-then-apply collapsed into one command.
//
// It pulls the latest EXPORT, not the latest document. Those differ the
// moment someone edits their design without exporting again, and a
// command that implied otherwise would have people wondering why their
// newest screen never arrived. Hence the line about when it was made.
var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Apply the latest Designer export to this project",
	Long: `Fetches the most recent export for this project and applies it.

Koolbase owns lib/generated/ and replaces it. Everything else is yours
and is never touched. If the generated tree has been edited by hand, or
holds a file Koolbase did not write, this stops and names them.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("not signed in: run `koolbase login` first")
		}
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			projectID = cfg.ProjectID
		}
		if projectID == "" {
			return fmt.Errorf("no project: pass --project, or run this inside a linked project")
		}
		into, _ := cmd.Flags().GetString("into")
		force, _ := cmd.Flags().GetBool("force")

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)

		latest, err := client.LatestExport(projectID)
		if err != nil {
			return err
		}
		fmt.Fprintf(
			cmd.OutOrStdout(),
			"Latest export %s, made %s\n",
			latest.ID, latest.CreatedAt.Local().Format("2 Jan 2006 15:04"),
		)

		zipBytes, err := client.DownloadExport(projectID, latest.ID)
		if err != nil {
			return err
		}

		tmp, err := os.CreateTemp("", "koolbase-export-*.zip")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(zipBytes); err != nil {
			tmp.Close()
			return err
		}
		tmp.Close()

		// The same apply the local-zip path uses. One implementation:
		// a pull and a manual apply must not differ in what they
		// protect.
		return applyExport(tmp.Name(), into, force, cmd.OutOrStdout())
	},
}

func init() {
	pullCmd.Flags().String("project", "", "project id (defaults to the linked project)")
	pullCmd.Flags().String("into", ".", "the Flutter project to apply into")
	pullCmd.Flags().Bool("force", false, "replace the generated tree even if it was edited")
	rootCmd.AddCommand(pullCmd)
}
