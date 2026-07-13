package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/kennedyowusu/koolbase-cli/internal/mcpserver"
)

// mcp: expose Koolbase to MCP-compatible AI clients (Claude, Cursor, ...).
// The server is a thin translation layer over the same API the CLI uses;
// all authorization stays server-side. Unlike other CLI commands, mcp serve
// REQUIRES KOOLBASE_API_KEY — it deliberately does not fall back to the
// interactive login session, so a misconfigured MCP client fails at spawn
// instead of silently connecting as the machine's logged-in user.
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run the Koolbase MCP server for AI clients",
	Long: "Expose Koolbase to MCP-compatible AI clients.\n\n" +
		"The server authenticates ONLY via KOOLBASE_API_KEY (an org-scoped\n" +
		"kb_live_ key); the key's scope is the hard ceiling on every tool.",
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the MCP server over stdio",
	Long: "Start the MCP server, speaking the Model Context Protocol over\n" +
		"stdin/stdout. An MCP client (Claude Desktop, Cursor, ...) launches\n" +
		"this process and communicates through the pipe.\n\n" +
		"stdout is reserved exclusively for protocol messages; all logs and\n" +
		"diagnostics go to stderr. KOOLBASE_API_KEY is required.",
	// stdout belongs to the protocol: never let cobra print usage there.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Fail closed: require the env key explicitly. Do NOT fall back to
		// the login-session credential chain like other commands do.
		key := os.Getenv("KOOLBASE_API_KEY")
		if key == "" {
			return fmt.Errorf(
				"KOOLBASE_API_KEY is not set — mcp serve refuses the " +
					"login-session fallback; set an org-scoped kb_live_ key",
			)
		}
		if !strings.HasPrefix(key, "kb_") {
			fmt.Fprintln(os.Stderr,
				"koolbase mcp: warning: KOOLBASE_API_KEY does not look like a kb_ key")
		}

		cfg, err := config.Load()
		if err != nil {
			return err
		}
		// config.Load already prefers the env key, but pin it explicitly so
		// the resolved principal can never be anything else.
		client := api.NewClient(cfg.BaseURL, key)

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
