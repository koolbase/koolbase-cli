package cmd

import (
	"fmt"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/kennedyowusu/koolbase-cli/internal/oauth"
	"github.com/spf13/cobra"
)

var connectCmd = &cobra.Command{
	Use:   "connect [github|google]",
	Short: "Connect an OAuth provider to your Koolbase account",
	Long: "Connect an additional sign-in provider (GitHub or Google) to your\n" +
		"already-authenticated Koolbase account. You must be logged in first\n" +
		"(run `koolbase login`).",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]
		if provider != "github" && provider != "google" {
			return fmt.Errorf("unsupported provider %q — use 'github' or 'google'", provider)
		}

		// Must already be logged in — connect attaches to the current account.
		cfg, err := config.Load()
		if err != nil || cfg.APIKey == "" {
			return fmt.Errorf("you must be logged in first — run `koolbase login`")
		}

		client := api.NewClient(cfg.BaseURL, cfg.APIKey)

		ctx := cmd.Context()
		switch provider {
		case "github":
			fmt.Println("Opening your browser to connect GitHub...")
			result, err := oauth.RunGitHub(ctx, oauth.GitHubConfig{
				ClientID:     githubClientID,
				ClientSecret: githubClientSecret,
			}, func(url string) {
				fmt.Printf("\nIf your browser didn't open, visit this URL to continue:\n\n%s\n\n", url)
			})
			if err != nil {
				return fmt.Errorf("github connect failed: %w", err)
			}
			if err := client.ConnectIdentity("github", result.AccessToken, ""); err != nil {
				return err
			}
		case "google":
			fmt.Println("Opening your browser to connect Google...")
			result, err := oauth.Run(ctx, oauth.Config{
				ClientID:     googleClientID,
				ClientSecret: googleClientSecret,
			}, func(url string) {
				fmt.Printf("\nIf your browser didn't open, visit this URL to continue:\n\n%s\n\n", url)
			})
			if err != nil {
				return fmt.Errorf("google connect failed: %w", err)
			}
			if err := client.ConnectIdentity("google", "", result.IDToken); err != nil {
				return err
			}
		}

		fmt.Printf("\n Connected %s to your account.\n", provider)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(connectCmd)
}
