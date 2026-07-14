package cmd

import (
	"fmt"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Sign out of your Koolbase account",
	Long: "Sign out of your Koolbase account. This invalidates your session on\n" +
		"the server and removes your saved credentials from this machine.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil || cfg.APIKey == "" {
			// Not logged in — nothing to do, but don't error noisily.
			fmt.Println("You're not signed in.")
			return nil
		}

		// Best-effort server-side invalidation. We clear local config either
		// way, so a network failure or already-expired session doesn't leave
		// the user stuck "logged in" locally.
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)
		if err := client.Logout(); err != nil {
			fmt.Println("Note: could not reach the server to end your session; clearing local credentials anyway.")
		}

		if err := config.Clear(); err != nil {
			return fmt.Errorf("failed to clear local credentials: %w", err)
		}

		fmt.Println("Signed out.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
