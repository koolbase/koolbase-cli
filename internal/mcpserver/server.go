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
	"sync"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version reports the CLI-wide version (see internal/version).
var Version = version.Version

// Server wraps the MCP server with the Koolbase API client all tools share.
type Server struct {
	mcp    *mcp.Server
	client *api.Client

	// orgID is the organization the configured key/session acts in, resolved
	// lazily via whoami and cached. Tools that address org-scoped endpoints
	// (e.g. list_projects) read it through orgOnce so the agent never has to
	// supply an org it can't see beyond anyway.
	orgOnce sync.Once
	orgID   string
	orgErr  error
	opts   Options
}

// Options gates optional tool families. Zero value = safe defaults.
type Options struct {
	// EnableCodepushMutations registers publish/recall patch tools.
	// Off by default: these change running apps on real devices, so the
	// operator must opt in per server instance (--enable-codepush-mutations).
	EnableCodepushMutations bool
}

// New constructs the Koolbase MCP server and registers all tools.
func New(client *api.Client, opts Options) *Server {
	s := &Server{
		mcp: mcp.NewServer(&mcp.Implementation{
			Name:    "koolbase",
			Title:   "Koolbase",
			Version: Version,
		}, nil),
		client: client,
		opts:   opts,
	}
	s.registerTools()
	return s
}

// org resolves and caches the caller's organization ID via whoami. The first
// call performs the lookup; subsequent calls return the cached value. Errors
// are cached too, so a broken key surfaces the same clear message every time
// rather than hammering the API.
func (s *Server) org() (string, error) {
	s.orgOnce.Do(func() {
		w, err := s.client.Whoami()
		if err != nil {
			s.orgErr = err
			return
		}
		s.orgID = w.OrgID
	})
	return s.orgID, s.orgErr
}

// Run serves MCP over stdio until the client disconnects or ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}
