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
	s.addListProjects()
	s.addGetProject()
	s.addListEnvironments()
	s.addListFlags()
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

// --- list_projects ----------------------------------------------------------

type projectSummary struct {
	ID   string `json:"id" jsonschema:"project UUID"`
	Name string `json:"name" jsonschema:"project name"`
}

type listProjectsOut struct {
	Projects []projectSummary `json:"projects" jsonschema:"projects in the caller's organization"`
}

func (s *Server) addListProjects() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "koolbase_list_projects",
		Description: "List all projects in the organization the server is acting for. Takes no arguments — the organization is determined by the configured API key.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listProjectsOut, error) {
		orgID, err := s.org()
		if err != nil {
			return nil, listProjectsOut{}, err
		}
		projects, err := s.client.ListProjects(orgID)
		if err != nil {
			return nil, listProjectsOut{}, err
		}
		out := listProjectsOut{Projects: make([]projectSummary, 0, len(projects))}
		for _, p := range projects {
			out.Projects = append(out.Projects, projectSummary{ID: p.ID, Name: p.Name})
		}
		return nil, out, nil
	})
}

// --- get_project ------------------------------------------------------------

type getProjectIn struct {
	ProjectID string `json:"project_id" jsonschema:"UUID of the project to fetch"`
}

type getProjectOut struct {
	ID        string `json:"id"`
	OrgID     string `json:"org_id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (s *Server) addGetProject() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "koolbase_get_project",
		Description: "Fetch a single Koolbase project by its UUID: identity, slug, and timestamps.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getProjectIn) (*mcp.CallToolResult, getProjectOut, error) {
		p, err := s.client.GetProject(in.ProjectID)
		if err != nil {
			return nil, getProjectOut{}, err
		}
		return nil, getProjectOut{
			ID: p.ID, OrgID: p.OrgID, Name: p.Name, Slug: p.Slug,
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		}, nil
	})
}

// --- list_flags -------------------------------------------------------------

type listFlagsIn struct {
	EnvironmentID string `json:"environment_id" jsonschema:"UUID of the environment whose flags to list. Resolve one via koolbase_list_environments if unknown."`
}

type flagSummary struct {
	ID                string `json:"id"`
	Key               string `json:"key" jsonschema:"the flag's programmatic key"`
	Enabled           bool   `json:"enabled"`
	RolloutPercentage int    `json:"rollout_percentage"`
	KillSwitch        bool   `json:"kill_switch"`
	Description       string `json:"description"`
}

type listFlagsOut struct {
	Flags []flagSummary `json:"flags"`
}

func (s *Server) addListFlags() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "koolbase_list_flags",
		Description: "List the feature flags of a Koolbase environment, with their enabled state, rollout percentage, and kill-switch status.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listFlagsIn) (*mcp.CallToolResult, listFlagsOut, error) {
		flags, err := s.client.ListFlags(in.EnvironmentID)
		if err != nil {
			return nil, listFlagsOut{}, err
		}
		out := listFlagsOut{Flags: make([]flagSummary, 0, len(flags))}
		for _, f := range flags {
			out.Flags = append(out.Flags, flagSummary{
				ID: f.ID, Key: f.Key, Enabled: f.Enabled,
				RolloutPercentage: f.RolloutPercentage,
				KillSwitch:        f.KillSwitch, Description: f.Description,
			})
		}
		return nil, out, nil
	})
}

// --- list_environments (helper for resolving env_ids) -----------------------

type listEnvironmentsIn struct {
	ProjectID string `json:"project_id" jsonschema:"UUID of the project whose environments to list"`
}

type environmentSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type listEnvironmentsOut struct {
	Environments []environmentSummary `json:"environments"`
}

func (s *Server) addListEnvironments() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "koolbase_list_environments",
		Description: "List the environments of a Koolbase project (e.g. production, staging). Use this to resolve an environment_id before calling environment-scoped tools like koolbase_list_flags.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listEnvironmentsIn) (*mcp.CallToolResult, listEnvironmentsOut, error) {
		envs, err := s.client.ListEnvironments(in.ProjectID)
		if err != nil {
			return nil, listEnvironmentsOut{}, err
		}
		out := listEnvironmentsOut{Environments: make([]environmentSummary, 0, len(envs))}
		for _, e := range envs {
			out.Environments = append(out.Environments, environmentSummary{ID: e.ID, Name: e.Name, Slug: e.Slug})
		}
		return nil, out, nil
	})
}
