package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/kennedyowusu/koolbase-cli/internal/config"
)

// whoami: show which account the CLI is authenticated as, verified LIVE
// against the API (a stale/revoked token reports as such instead of
// surfacing later as an opaque "unauthorized" mid-command — Phase 8
// dogfood: a stale wrong-org token cost a debugging detour).
var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the account this CLI is logged in as",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.APIKey == "" {
			fmt.Println("Not logged in. Run `koolbase login`.")
			return nil
		}
		req, err := http.NewRequest("GET", cfg.BaseURL+"/v1/me", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("could not reach %s: %w", cfg.BaseURL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			hint := ""
			if cfg.Email != "" {
				hint = fmt.Sprintf(" (last login: %s)", cfg.Email)
			}
			fmt.Printf("Session is invalid or expired%s. Run `koolbase login`.\n", hint)
			return nil
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected response %d from %s", resp.StatusCode, cfg.BaseURL)
		}
		body, _ := io.ReadAll(resp.Body)
		var user struct {
			Email string `json:"email"`
			Role  string `json:"role"`
			OrgID string `json:"org_id"`
		}
		if err := json.Unmarshal(body, &user); err != nil || user.Email == "" {
			return fmt.Errorf("could not parse account info")
		}
		fmt.Printf("Logged in as %s (role: %s, org: %s)\n", user.Email, user.Role, user.OrgID)
		fmt.Printf("API: %s\n", cfg.BaseURL)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(whoamiCmd)
}
