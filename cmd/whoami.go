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
		req, err := http.NewRequest("GET", cfg.BaseURL+"/v1/whoami", nil)
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
			fmt.Printf("Credentials rejected%s — the API key or session is invalid, expired, or revoked. Run `koolbase login` or set a valid KOOLBASE_API_KEY.\n", hint)
			return nil
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected response %d from %s", resp.StatusCode, cfg.BaseURL)
		}
		body, _ := io.ReadAll(resp.Body)
		var who struct {
			Type    string `json:"type"`
			OrgID   string `json:"org_id"`
			OrgName string `json:"org_name"`
			Email   string `json:"email"`
			Role    string `json:"role"`
			Scope   string `json:"scope"`
			KeyID   string `json:"key_id"`
		}
		if err := json.Unmarshal(body, &who); err != nil || who.Type == "" {
			return fmt.Errorf("could not parse account info")
		}
		org := who.OrgName
		if org == "" {
			org = who.OrgID
		}
		switch who.Type {
		case "api_key":
			fmt.Printf("Authenticated with an API key (scope: %s, org: %s)\n", who.Scope, org)
			fmt.Printf("Key ID: %s\n", who.KeyID)
		default:
			fmt.Printf("Logged in as %s (role: %s, org: %s)\n", who.Email, who.Role, org)
		}
		fmt.Printf("API: %s\n", cfg.BaseURL)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(whoamiCmd)
}
