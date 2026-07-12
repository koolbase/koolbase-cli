// Package mcpserver implements the Koolbase MCP server: a thin translation
// layer exposing Koolbase control-plane operations as MCP tools.
//
// Architecture rules (deliberate, do not erode):
//
//   - Every tool call goes through the existing internal/api client to the
//     production API. No direct DB access, no business logic here — the
//     backend's authorization (org ownership, key scopes, plan limits) stays
//     authoritative.
//   - Auth is the CLI's config chain: ~/.koolbase/config.json overridden by
//     KOOLBASE_API_KEY. MCP deployments should set KOOLBASE_API_KEY to an
//     org-scoped kb_live_ key; the key's scope (read < write < admin) is the
//     hard ceiling on what any tool can do, enforced server-side.
//   - stdout belongs exclusively to the MCP protocol. All logging goes to
//     stderr. One stray print to stdout corrupts the JSON-RPC stream.
package mcpserver

import (
	"context"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is stamped by the CLI build; fallback for dev builds.
var Version = "dev"

// Server wraps the MCP server with the Koolbase API client all tools share.
type Server struct {
	mcp    *mcp.Server
	client *api.Client
}

// New constructs the Koolbase MCP server and registers all tools.
func New(client *api.Client) *Server {
	s := &Server{
		mcp: mcp.NewServer(&mcp.Implementation{
			Name:    "koolbase",
			Title:   "Koolbase",
			Version: Version,
		}, nil),
		client: client,
	}
	s.registerTools()
	return s
}

// Run serves MCP over stdio until the client disconnects or ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}
