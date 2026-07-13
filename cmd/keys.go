package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
)

// keysClient builds an authenticated client and resolves the caller's org
// via /v1/whoami. Shared by all keys subcommands.
func keysClient() (*api.Client, *api.WhoAmI, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	client := api.NewClient(cfg.BaseURL, cfg.APIKey)
	who, err := client.Whoami()
	if err != nil {
		return nil, nil, fmt.Errorf("could not resolve your account (are you logged in? try `koolbase login`): %w", err)
	}
	return client, who, nil
}

var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Manage organization API keys",
	Long: "Create, list, and revoke organization API keys.\n\n" +
		"Keys authenticate the MCP server, CI pipelines, and other\n" +
		"programmatic access. Creating a key requires a logged-in dashboard\n" +
		"session (`koolbase login`) — API keys cannot mint other keys.",
}

var keysCreateName string
var keysCreateScope string

var keysCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new API key (secret shown ONCE)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if keysCreateName == "" {
			return fmt.Errorf("--name is required")
		}
		switch keysCreateScope {
		case "read", "write", "admin":
		default:
			return fmt.Errorf("--scope must be read, write, or admin")
		}

		client, who, err := keysClient()
		if err != nil {
			return err
		}
		if who.Type == "api_key" {
			return fmt.Errorf("you are authenticated with an API key — keys cannot mint keys. Run `koolbase login` first")
		}

		resp, err := client.MintKey(who.OrgID, keysCreateName, keysCreateScope)
		if err != nil {
			return err
		}

		fmt.Printf("\n✓ Key created: %s (scope: %s, org: %s)\n\n", resp.Key.Name, resp.Key.Scope, who.OrgName)
		fmt.Printf("  %s\n\n", resp.Secret)
		fmt.Println("  ⚠ This secret is shown ONCE and cannot be retrieved again.")
		fmt.Println("    Store it in a password manager now.")
		fmt.Println("\nFor Claude Desktop / MCP, add to your MCP config:")
		fmt.Printf(`
  {
    "mcpServers": {
      "koolbase": {
        "command": "/usr/local/bin/koolbase",
        "args": ["mcp", "serve"],
        "env": { "KOOLBASE_API_KEY": "%s" }
      }
    }
  }

`, resp.Secret)
		return nil
	},
}

var keysListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your organization's API keys",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, who, err := keysClient()
		if err != nil {
			return err
		}
		keys, err := client.ListKeys(who.OrgID)
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			fmt.Println("No API keys. Create one with: koolbase keys create --name <name> --scope <read|write|admin>")
			return nil
		}
		fmt.Printf("%-38s %-20s %-14s %-7s %-9s %s\n", "ID", "NAME", "PREFIX", "SCOPE", "STATUS", "LAST USED")
		fmt.Printf("%-38s %-20s %-14s %-7s %-9s %s\n", "--", "----", "------", "-----", "------", "---------")
		for _, k := range keys {
			status := "active"
			if k.RevokedAt != nil {
				status = "revoked"
			}
			lastUsed := "never"
			if k.LastUsedAt != nil {
				lastUsed = *k.LastUsedAt
			}
			fmt.Printf("%-38s %-20s %-14s %-7s %-9s %s\n", k.ID, k.Name, k.KeyPrefix, k.Scope, status, lastUsed)
		}
		return nil
	},
}

var keysRevokeCmd = &cobra.Command{
	Use:   "revoke <key_id>",
	Short: "Revoke an API key (takes effect immediately)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, who, err := keysClient()
		if err != nil {
			return err
		}
		fmt.Printf("Revoke key %s? Anything using it stops working immediately. [y/N]: ", args[0])
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			fmt.Println("Cancelled.")
			return nil
		}
		if err := client.RevokeKey(who.OrgID, args[0]); err != nil {
			return err
		}
		fmt.Printf("✓ Key %s revoked\n", args[0])
		return nil
	},
}

func init() {
	keysCreateCmd.Flags().StringVar(&keysCreateName, "name", "", "Human-readable key name (required)")
	keysCreateCmd.Flags().StringVar(&keysCreateScope, "scope", "read", "Key scope: read, write, or admin")
	keysCmd.AddCommand(keysCreateCmd, keysListCmd, keysRevokeCmd)
	rootCmd.AddCommand(keysCmd)
}
