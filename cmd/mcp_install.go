package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

var mcpInstallKey string
var mcpInstallMutations bool

// claudeConfigPath returns Claude Desktop's config file location per OS.
func claudeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"), nil
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Claude", "claude_desktop_config.json"), nil
	default: // linux and friends
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"), nil
	}
}

var mcpInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Wire the Koolbase MCP server into Claude Desktop",
	Long: "Adds (or updates) the koolbase entry in Claude Desktop's MCP config,\n" +
		"pointing at this binary with the provided API key. Existing servers in\n" +
		"the config are preserved; the previous file is backed up alongside it.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if mcpInstallKey == "" {
			return fmt.Errorf("--key is required. Mint one with:\n\n  koolbase keys create --name claude-desktop --scope write\n")
		}
		if !strings.HasPrefix(mcpInstallKey, "kb_") {
			fmt.Fprintln(os.Stderr, "  ⚠ the key does not look like a kb_ management key")
		}

		binPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("could not resolve this binary's path: %w", err)
		}
		binPath, err = filepath.EvalSymlinks(binPath)
		if err != nil {
			return fmt.Errorf("could not resolve this binary's path: %w", err)
		}

		cfgPath, err := claudeConfigPath()
		if err != nil {
			return err
		}

		// Load existing config (tolerate absence); preserve everything.
		root := map[string]json.RawMessage{}
		if data, rerr := os.ReadFile(cfgPath); rerr == nil {
			if jerr := json.Unmarshal(data, &root); jerr != nil {
				return fmt.Errorf("%s exists but is not valid JSON — fix or remove it first: %w", cfgPath, jerr)
			}
			backup := cfgPath + ".bak"
			if werr := os.WriteFile(backup, data, 0600); werr == nil {
				fmt.Printf("  backed up existing config to %s\n", backup)
			}
		}

		servers := map[string]json.RawMessage{}
		if raw, ok := root["mcpServers"]; ok {
			if jerr := json.Unmarshal(raw, &servers); jerr != nil {
				return fmt.Errorf("mcpServers in %s is malformed: %w", cfgPath, jerr)
			}
		}

		mcpArgs := []string{"mcp", "serve"}
		if mcpInstallMutations {
			mcpArgs = append(mcpArgs, "--enable-codepush-mutations")
		}
		entry := map[string]any{
			"command": binPath,
			"args":    mcpArgs,
			"env":     map[string]string{"KOOLBASE_API_KEY": mcpInstallKey},
		}
		entryJSON, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		servers["koolbase"] = entryJSON

		serversJSON, err := json.Marshal(servers)
		if err != nil {
			return err
		}
		root["mcpServers"] = serversJSON

		out, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(cfgPath, append(out, '\n'), 0600); err != nil {
			return err
		}

		fmt.Printf("\n✓ Koolbase MCP server installed for Claude Desktop\n")
		fmt.Printf("  config: %s\n  binary: %s\n", cfgPath, binPath)
		if mcpInstallMutations {
			fmt.Println("  code-push mutation tools: ENABLED (requires an admin-scoped key)")
		}
		fmt.Println("\nNow fully quit Claude Desktop (Cmd-Q on macOS) and reopen it.")
		fmt.Println("Then ask: \"What projects do I have on Koolbase?\"")
		return nil
	},
}

func init() {
	mcpInstallCmd.Flags().StringVar(&mcpInstallKey, "key", "", "Koolbase API key for the assistant (kb_live_...)")
	mcpInstallCmd.Flags().BoolVar(&mcpInstallMutations, "codepush-mutations", false, "Enable the code-push publish/recall tools (admin key required)")
	mcpCmd.AddCommand(mcpInstallCmd)
}
