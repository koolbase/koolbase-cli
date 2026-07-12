package api

import (
	"encoding/json"
	"fmt"
)

// WhoAmI mirrors the GET /v1/whoami response: the authenticated principal.
// For key principals Scope is the ceiling the MCP server reads at startup.
type WhoAmI struct {
	Type    string `json:"type"`
	OrgID   string `json:"org_id"`
	OrgName string `json:"org_name"`
	Scope   string `json:"scope,omitempty"`
	KeyID   string `json:"key_id,omitempty"`
	UserID  string `json:"user_id,omitempty"`
	Email   string `json:"email,omitempty"`
	Role    string `json:"role,omitempty"`
}

// Whoami returns the principal behind the configured credentials.
func (c *Client) Whoami() (*WhoAmI, error) {
	data, status, err := c.do("GET", "/v1/whoami", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("whoami failed (%d): %s", status, string(data))
	}
	var w WhoAmI
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, err
	}
	return &w, nil
}
