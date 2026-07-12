package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools wires every MCP tool onto the server. Tools are added in
// ascending order of privilege; each is a thin adapter over the existing
// api.Client, so the backend's authorization (org ownership, key scope,
// plan limits) stays authoritative. No business logic lives here.
func (s *Server) registerTools() {
	s.addWhoami()
}

// --- whoami -----------------------------------------------------------------

// whoamiOut is the structured result of the whoami tool. Field docs become
// the output schema via jsonschema struct tags.
type whoamiOut struct {
	Type    string `json:"type" jsonschema:"principal kind: api_key or user"`
	OrgID   string `json:"org_id" jsonschema:"the organization this principal acts in"`
	OrgName string `json:"org_name" jsonschema:"human-readable organization name"`
	Scope   string `json:"scope,omitempty" jsonschema:"for api_key principals: read, write, or admin — the ceiling on all operations"`
}

// addWhoami registers the whoami tool: returns the identity and scope of the
// key or session the server is running as. Read-only, no input. This is also
// the server's own startup probe to learn its scope ceiling.
func (s *Server) addWhoami() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "koolbase_whoami",
		Description: "Return the identity the Koolbase MCP server is acting as: principal type, organization, and (for API keys) the scope ceiling that limits what every other tool can do.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, whoamiOut, error) {
		w, err := s.client.Whoami()
		if err != nil {
			return nil, whoamiOut{}, err
		}
		return nil, whoamiOut{
			Type:    w.Type,
			OrgID:   w.OrgID,
			OrgName: w.OrgName,
			Scope:   w.Scope,
		}, nil
	})
}
