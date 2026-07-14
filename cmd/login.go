package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/kennedyowusu/koolbase-cli/internal/oauth"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Google Desktop-app OAuth client for the Koolbase CLI.

const googleClientID = "225474284307-v7ujjcealk7t5a1g3tblg52vsi6e3r0c.apps.googleusercontent.com"

var googleClientSecret string

// GitHub OAuth app for the Koolbase CLI.
const githubClientID = "Ov23liXTp5SuXHTUvgIk"

var githubClientSecret string

var loginUsePassword bool
var loginUseGitHub bool

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with your Koolbase account",
	Long: "Authenticate with your Koolbase account.\n\n" +
		"By default this opens your browser to sign in with Google.\n" +
		"Use --password to sign in with an email and password instead.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if loginUsePassword {
			return runPasswordLogin()
		}
		if loginUseGitHub {
			return runGitHubLogin(cmd.Context())
		}
		return runGoogleLogin(cmd.Context())
	},
}

// runGoogleLogin performs the browser-based OAuth flow: it opens Google's
// consent screen, receives the redirect on a local loopback port, and
// exchanges the result for a Koolbase session.
func runGoogleLogin(ctx context.Context) error {
	fmt.Println("Opening your browser to sign in with Google...")

	result, err := oauth.Run(ctx, oauth.Config{
		ClientID:     googleClientID,
		ClientSecret: googleClientSecret,
	}, func(url string) {
		fmt.Printf("\nIf your browser didn't open, visit this URL to continue:\n\n%s\n\n", url)
	})
	if err != nil {
		return fmt.Errorf("google sign-in failed: %w", err)
	}

	client := api.NewClient("", "")
	resp, err := client.LoginWithGoogle(result.IDToken)
	if err != nil {
		return err
	}

	if err := saveSession(resp); err != nil {
		return err
	}

	fmt.Printf("\n Logged in as %s\n", resp.User.Email)
	fmt.Println("Run `koolbase functions list --project <project_id>` to see your functions.")
	return nil
}

// runGitHubLogin performs the browser-based GitHub OAuth flow: it opens
// GitHub's consent screen, receives the redirect on a local loopback port, and
// exchanges the result for a Koolbase session. GitHub is OAuth2 (not OIDC), so
// the flow yields an access token the API verifies against GitHub's API.
func runGitHubLogin(ctx context.Context) error {
	fmt.Println("Opening your browser to sign in with GitHub...")

	result, err := oauth.RunGitHub(ctx, oauth.GitHubConfig{
		ClientID:     githubClientID,
		ClientSecret: githubClientSecret,
	}, func(url string) {
		fmt.Printf("\nIf your browser didn't open, visit this URL to continue:\n\n%s\n\n", url)
	})
	if err != nil {
		return fmt.Errorf("github sign-in failed: %w", err)
	}

	client := api.NewClient("", "")
	resp, err := client.LoginWithGitHub(result.AccessToken)
	if err != nil {
		return err
	}

	if err := saveSession(resp); err != nil {
		return err
	}

	fmt.Printf("\n Logged in as %s\n", resp.User.Email)
	fmt.Println("Run `koolbase functions list --project <project_id>` to see your functions.")
	return nil
}

// runPasswordLogin performs the classic email + password login.

// runPasswordLogin performs the classic email + password login. OAuth-only
// accounts (no password) are surfaced with an actionable message by the API's
// oauth_only_account error, which the API client turns into readable text.
func runPasswordLogin() error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Email: ")
	email, _ := reader.ReadString('\n')
	email = strings.TrimSpace(email)

	fmt.Print("Password: ")
	passwordBytes, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("failed to read password: %w", err)
	}
	password := strings.TrimSpace(string(passwordBytes))

	client := api.NewClient("", "")
	resp, err := client.Login(email, password)
	if err != nil {
		return err
	}

	if err := saveSession(resp); err != nil {
		return err
	}

	fmt.Printf("\n Logged in as %s\n", resp.User.Email)
	fmt.Println("Run `koolbase functions list --project <project_id>` to see your functions.")
	return nil
}

// saveSession persists the session token and identity to the CLI config.
func saveSession(resp *api.LoginResponse) error {
	cfg := &config.Config{
		APIKey:  resp.AccessToken,
		Email:   resp.User.Email,
		BaseURL: "https://api.koolbase.com",
	}
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	return nil
}

func init() {
	loginCmd.Flags().BoolVar(&loginUsePassword, "password", false, "Sign in with email and password instead of Google")
	loginCmd.Flags().BoolVar(&loginUseGitHub, "github", false, "Sign in with GitHub instead of Google")
}
