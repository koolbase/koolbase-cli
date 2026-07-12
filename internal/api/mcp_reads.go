package api

import (
	"encoding/json"
	"fmt"
)

// ProjectDetail is the useful subset of GET /v1/projects/{id}. Branding and
// email-template fields are intentionally omitted — an agent reasoning about
// projects needs identity and timestamps, not SMTP config.
type ProjectDetail struct {
	ID        string `json:"id"`
	OrgID     string `json:"org_id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// GetProject fetches a single project by ID.
func (c *Client) GetProject(projectID string) (*ProjectDetail, error) {
	data, status, err := c.do("GET", "/v1/projects/"+projectID, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("get project failed (%d): %s", status, string(data))
	}
	var p ProjectDetail
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Environment is the subset of a project environment an agent needs to
// address env-scoped resources (flags, configs).
type Environment struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
}

// ListEnvironments returns the environments of a project. Needed so an agent
// can resolve an env_id before calling env-scoped tools like list_flags.
func (c *Client) ListEnvironments(projectID string) ([]Environment, error) {
	data, status, err := c.do("GET", "/v1/projects/"+projectID+"/environments", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("list environments failed (%d): %s", status, string(data))
	}
	var envs []Environment
	if err := json.Unmarshal(data, &envs); err != nil {
		return nil, err
	}
	return envs, nil
}

// Flag is a feature flag as returned by GET /v1/environments/{env_id}/flags.
type Flag struct {
	ID                string `json:"id"`
	EnvironmentID     string `json:"environment_id"`
	Key               string `json:"key"`
	Enabled           bool   `json:"enabled"`
	RolloutPercentage int    `json:"rollout_percentage"`
	KillSwitch        bool   `json:"kill_switch"`
	Description       string `json:"description"`
}

// ListFlags returns the feature flags of an environment.
func (c *Client) ListFlags(envID string) ([]Flag, error) {
	data, status, err := c.do("GET", "/v1/environments/"+envID+"/flags", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("list flags failed (%d): %s", status, string(data))
	}
	var flags []Flag
	if err := json.Unmarshal(data, &flags); err != nil {
		return nil, err
	}
	return flags, nil
}

// UpdateFlagRequest is the full-replace body PUT /flags/{flag_id} expects.
// All four fields are always sent; callers wanting partial semantics must
// read current state and merge before calling (the MCP set_flag tool does).
type UpdateFlagRequest struct {
	Enabled           bool   `json:"enabled"`
	RolloutPercentage int    `json:"rollout_percentage"`
	KillSwitch        bool   `json:"kill_switch"`
	Description       string `json:"description"`
}

// UpdateFlag replaces a feature flag's mutable fields. Returns the updated
// flag. A read-scoped key receives 403 insufficient_scope here (surfaced by
// the status/body in the error), which callers should relay clearly.
func (c *Client) UpdateFlag(flagID string, req UpdateFlagRequest) (*Flag, error) {
	data, status, err := c.do("PUT", "/v1/flags/"+flagID, req)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("update flag failed (%d): %s", status, string(data))
	}
	var f Flag
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}
