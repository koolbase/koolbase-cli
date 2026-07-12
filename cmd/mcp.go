package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/kennedyowusu/koolbase-cli/internal/mcpserver"
)

// mcp: expose Koolbase to MCP-compatible AI clients (Claude, Cursor, ...).
// The server is a thin translation layer over the same API the CLI uses;
// all authorization stays server-side. Auth is the CLI's credential chain —
// for MCP deployments set KOOLBASE_API_KEY to an org-scoped kb_live_ key,
// whose scope (read < write < admin) caps every tool.
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run the Koolbase MCP server for AI clients",
	Long: "Expose Koolbase to MCP-compatible AI clients.\n\n" +
		"The server authenticates with the CLI's configured credentials. For\n" +
		"agent use, set KOOLBASE_API_KEY to an org-scoped kb_live_ key; the\n" +
		"key's scope is the hard ceiling on every tool the agent can call.",
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the MCP server over stdio",
	Long: "Start the MCP server, speaking the Model Context Protocol over\n" +
		"stdin/stdout. An MCP client (Claude Desktop, Cursor, ...) launches\n" +
		"this process and communicates through the pipe.\n\n" +
		"stdout is reserved exclusively for protocol messages; all logs and\n" +
		"diagnostics go to stderr.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)

		// Signal-aware context: Ctrl-C / SIGTERM cleanly stops the server.
		ctx, stop := signal.NotifyContext(
			context.Background(), os.Interrupt, syscall.SIGTERM,
		)
		defer stop()

		// Diagnostics to stderr only — stdout belongs to the protocol.
		fmt.Fprintln(os.Stderr, "koolbase mcp: serving over stdio")

		srv := mcpserver.New(client)
		if err := srv.Run(ctx); err != nil && ctx.Err() == nil {
			return fmt.Errorf("mcp server: %w", err)
		}
		return nil
	},
}

func init() {
	mcpCmd.AddCommand(mcpServeCmd)
	rootCmd.AddCommand(mcpCmd)
}
