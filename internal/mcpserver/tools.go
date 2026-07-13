package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/kennedyowusu/koolbase-cli/internal/api"
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
	s.addSetFlag()
	s.addListConfigs()
	s.addSetConfig()
	s.addListPatches()
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

// --- set_flag (first write tool) --------------------------------------------

type setFlagIn struct {
	EnvironmentID     string  `json:"environment_id" jsonschema:"UUID of the environment the flag belongs to"`
	FlagID            string  `json:"flag_id" jsonschema:"UUID of the flag to update (from koolbase_list_flags)"`
	Enabled           *bool   `json:"enabled,omitempty" jsonschema:"if set, turn the flag on or off; omit to leave unchanged"`
	RolloutPercentage *int    `json:"rollout_percentage,omitempty" jsonschema:"if set, target rollout 0-100; omit to leave unchanged"`
	KillSwitch        *bool   `json:"kill_switch,omitempty" jsonschema:"if set, engage or release the kill switch; omit to leave unchanged"`
	Description       *string `json:"description,omitempty" jsonschema:"if set, replace the description; omit to leave unchanged"`
}

type setFlagOut struct {
	ID                string `json:"id"`
	Key               string `json:"key"`
	Enabled           bool   `json:"enabled"`
	RolloutPercentage int    `json:"rollout_percentage"`
	KillSwitch        bool   `json:"kill_switch"`
	Description       string `json:"description"`
}

// addSetFlag registers the first write tool. It merges the caller's specified
// fields onto the flag's current state before PUTting, so an agent can change
// one field without resetting the others (the API route is a full replace).
//
// Requires a write-scoped key; a read key yields 403 insufficient_scope from
// the API, which this tool rewrites into an actionable message.
func (s *Server) addSetFlag() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "koolbase_set_flag",
		Description: "Update a Koolbase feature flag's state (enabled, rollout percentage, kill switch, description). " +
			"Only the fields you provide are changed; others keep their current values. " +
			"This changes live app behavior for users on this environment. Requires a write-scoped API key.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in setFlagIn) (*mcp.CallToolResult, setFlagOut, error) {
		// Read current state so omitted fields are preserved (route is a full replace).
		flags, err := s.client.ListFlags(in.EnvironmentID)
		if err != nil {
			return nil, setFlagOut{}, mapScopeErr(err)
		}
		var cur *api.Flag
		for i := range flags {
			if flags[i].ID == in.FlagID {
				cur = &flags[i]
				break
			}
		}
		if cur == nil {
			return nil, setFlagOut{}, fmt.Errorf("flag %s not found in environment %s", in.FlagID, in.EnvironmentID)
		}

		// Merge: start from current, overlay only what the caller specified.
		req := api.UpdateFlagRequest{
			Enabled:           cur.Enabled,
			RolloutPercentage: cur.RolloutPercentage,
			KillSwitch:        cur.KillSwitch,
			Description:       cur.Description,
		}
		if in.Enabled != nil {
			req.Enabled = *in.Enabled
		}
		if in.RolloutPercentage != nil {
			req.RolloutPercentage = *in.RolloutPercentage
		}
		if in.KillSwitch != nil {
			req.KillSwitch = *in.KillSwitch
		}
		if in.Description != nil {
			req.Description = *in.Description
		}

		updated, err := s.client.UpdateFlag(in.FlagID, req)
		if err != nil {
			return nil, setFlagOut{}, mapScopeErr(err)
		}
		return nil, setFlagOut{
			ID: updated.ID, Key: updated.Key, Enabled: updated.Enabled,
			RolloutPercentage: updated.RolloutPercentage,
			KillSwitch:        updated.KillSwitch, Description: updated.Description,
		}, nil
	})
}

// mapScopeErr rewrites the API's 403 insufficient_scope into an actionable
// message naming the fix, and passes other errors through unchanged.
func mapScopeErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "insufficient_scope") || strings.Contains(msg, "(403)") {
		return fmt.Errorf("this API key lacks write permission for this operation. " +
			"Mint a write-scoped key in the Koolbase dashboard and set KOOLBASE_API_KEY to it")
	}
	return err
}

// decodeJSON turns a json.RawMessage into a plain any (string, float64, bool,
// map, or slice) so it serializes as its real JSON value rather than raw
// bytes. On malformed input it returns nil rather than failing the tool.
func decodeJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

// --- list_configs -----------------------------------------------------------

type listConfigsIn struct {
	EnvironmentID string `json:"environment_id" jsonschema:"UUID of the environment whose remote configs to list"`
}

type configSummary struct {
	ID          string `json:"id"`
	Key         string `json:"key" jsonschema:"the config's programmatic key"`
	Value       any    `json:"value" jsonschema:"current value (string, number, boolean, or object per value_type)"`
	ValueType   string `json:"value_type" jsonschema:"string, number, boolean, or json"`
	Description string `json:"description"`
}

type listConfigsOut struct {
	Configs []configSummary `json:"configs"`
}

func (s *Server) addListConfigs() {
	// The Value field is `any`; the SDK infers it as the boolean `true`
	// schema, which Claude Desktop's validator rejects (dropping ALL tools).
	// Build the schema ourselves and force an object-form accept-anything
	// schema for `value`.
	outSchema, serr := jsonschema.For[listConfigsOut](nil)
	if serr == nil && outSchema.Properties["configs"] != nil && outSchema.Properties["configs"].Items != nil {
		outSchema.Properties["configs"].Items.Properties["value"] = &jsonschema.Schema{
			Description: "current value (string, number, boolean, or object per value_type)",
		}
	}
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:         "koolbase_list_configs",
		Description:  "List the remote-config entries of a Koolbase environment, with their current values and types. Remote config lets you change app behavior/content without shipping an app update.",
		OutputSchema: outSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listConfigsIn) (*mcp.CallToolResult, listConfigsOut, error) {
		configs, err := s.client.ListConfigs(in.EnvironmentID)
		if err != nil {
			return nil, listConfigsOut{}, mapScopeErr(err)
		}
		out := listConfigsOut{Configs: make([]configSummary, 0, len(configs))}
		for _, c := range configs {
			out.Configs = append(out.Configs, configSummary{
				ID: c.ID, Key: c.Key, Value: decodeJSON(c.Value),
				ValueType: c.ValueType, Description: c.Description,
			})
		}
		return nil, out, nil
	})
}

// --- set_config -------------------------------------------------------------

type setConfigIn struct {
	EnvironmentID string  `json:"environment_id" jsonschema:"UUID of the environment the config belongs to"`
	ConfigID      string  `json:"config_id" jsonschema:"UUID of the config to update (from koolbase_list_configs)"`
	Value         *string `json:"value,omitempty" jsonschema:"if set, the new value as a JSON literal matching the config's type: a quoted string like \"hello\", a number like 42, a boolean true/false, or a JSON object/array. Omit to leave unchanged."`
	Description   *string `json:"description,omitempty" jsonschema:"if set, replace the description; omit to leave unchanged"`
}

type setConfigOut struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Value       any    `json:"value"`
	ValueType   string `json:"value_type"`
	Description string `json:"description"`
}

func (s *Server) addSetConfig() {
	// Same `any`-typed Value as list_configs: force an object-form schema
	// so Claude Desktop's validator accepts the manifest.
	outSchema, serr := jsonschema.For[setConfigOut](nil)
	if serr == nil && outSchema.Properties != nil {
		outSchema.Properties["value"] = &jsonschema.Schema{
			Description: "the config's value after the update",
		}
	}
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "koolbase_set_config",
		OutputSchema: outSchema,
		Description: "Update a Koolbase remote-config value or description. Only fields you provide change; others keep current values. " +
			"The value must be a JSON literal matching the config's value_type (string/number/boolean/json) — the server rejects a type mismatch. " +
			"This changes live app behavior/content for users on this environment. Requires a write-scoped API key.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in setConfigIn) (*mcp.CallToolResult, setConfigOut, error) {
		configs, err := s.client.ListConfigs(in.EnvironmentID)
		if err != nil {
			return nil, setConfigOut{}, mapScopeErr(err)
		}
		var cur *api.Config
		for i := range configs {
			if configs[i].ID == in.ConfigID {
				cur = &configs[i]
				break
			}
		}
		if cur == nil {
			return nil, setConfigOut{}, fmt.Errorf("config %s not found in environment %s", in.ConfigID, in.EnvironmentID)
		}

		req := api.UpdateConfigRequest{
			Value:       cur.Value,
			Description: cur.Description,
		}
		if in.Value != nil {
			// Validate the caller's value is well-formed JSON before sending;
			// the server additionally checks it against the config's type.
			raw := json.RawMessage(*in.Value)
			if !json.Valid(raw) {
				return nil, setConfigOut{}, fmt.Errorf("value is not valid JSON: %q — use a JSON literal like \"text\", 42, true, or {\"k\":\"v\"}", *in.Value)
			}
			req.Value = raw
		}
		if in.Description != nil {
			req.Description = *in.Description
		}

		updated, err := s.client.UpdateConfig(in.ConfigID, req)
		if err != nil {
			return nil, setConfigOut{}, mapScopeErr(err)
		}
		return nil, setConfigOut{
			ID: updated.ID, Key: updated.Key, Value: decodeJSON(updated.Value),
			ValueType: updated.ValueType, Description: updated.Description,
		}, nil
	})
}

// --- list_patches -----------------------------------------------------------

type listPatchesIn struct {
	ProjectID string `json:"project_id" jsonschema:"UUID of the project (app) whose code-push patches to list"`
	ReleaseID string `json:"release_id,omitempty" jsonschema:"optional: limit to one release's patches. Omit to list patches across all releases of the app."`
}

type patchSummary struct {
	PatchID           string `json:"patch_id"`
	ReleaseID         string `json:"release_id"`
	BuildID           string `json:"build_id" jsonschema:"the base build these patches apply on top of"`
	Platform          string `json:"platform"`
	AppVersion        string `json:"app_version"`
	PatchNumber       int    `json:"patch_number"`
	Status            string `json:"status" jsonschema:"draft, published, or recalled"`
	RolloutPercentage int    `json:"rollout_percentage"`
	Mandatory         bool   `json:"mandatory"`
	SizeBytes         int    `json:"size_bytes"`
	ReleaseNotes      string `json:"release_notes,omitempty"`
	CreatedAt         string `json:"created_at"`
	PublishedAt       string `json:"published_at,omitempty"`
	RecalledAt        string `json:"recalled_at,omitempty"`
}

type listPatchesOut struct {
	Patches []patchSummary `json:"patches"`
}

func (s *Server) addListPatches() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "koolbase_list_patches",
		Description: "List code-push (OTA) patches for a Koolbase app, with their status (draft/published/recalled), rollout percentage, and target build. " +
			"Omit release_id to see patches across all releases. Read-only — does not publish or recall anything.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listPatchesIn) (*mcp.CallToolResult, listPatchesOut, error) {
		// Resolve which releases to scan: one, or all for the app.
		var releases []api.Release
		if in.ReleaseID != "" {
			releases = []api.Release{{ID: in.ReleaseID}}
		} else {
			rels, err := s.client.ListReleases(in.ProjectID)
			if err != nil {
				return nil, listPatchesOut{}, mapScopeErr(err)
			}
			releases = rels
		}

		// Index release metadata (build_id/platform/version) to enrich patches.
		relMeta := make(map[string]api.Release, len(releases))
		for _, r := range releases {
			relMeta[r.ID] = r
		}

		out := listPatchesOut{Patches: []patchSummary{}}
		for _, r := range releases {
			patches, err := s.client.ListPatches(in.ProjectID, r.ID)
			if err != nil {
				return nil, listPatchesOut{}, mapScopeErr(err)
			}
			meta := relMeta[r.ID]
			for _, p := range patches {
				out.Patches = append(out.Patches, patchSummary{
					PatchID: p.ID, ReleaseID: p.ReleaseID,
					BuildID: meta.BuildID, Platform: meta.Platform, AppVersion: meta.AppVersion,
					PatchNumber: p.PatchNumber, Status: p.Status,
					RolloutPercentage: p.RolloutPercentage, Mandatory: p.Mandatory,
					SizeBytes: p.SizeBytes, ReleaseNotes: p.ReleaseNotes,
					CreatedAt: p.CreatedAt, PublishedAt: p.PublishedAt, RecalledAt: p.RecalledAt,
				})
			}
		}
		return nil, out, nil
	})
}
