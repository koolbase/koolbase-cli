package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// ReplayResult is the server's report of a trigger replay (or dry run).
type ReplayResult struct {
	TriggerID        string `json:"trigger_id"`
	FunctionName     string `json:"function_name"`
	EventType        string `json:"event_type"`
	Collection       string `json:"collection"`
	DryRun           bool   `json:"dry_run"`
	Matching         int    `json:"matching"`
	Queued           int    `json:"queued"`
	Remaining        int    `json:"remaining"`
	EstimatedSeconds int    `json:"estimated_seconds"`
}

// ReplayTrigger asks the server to queue the trigger's function for the
// records already in its collection. limit 0 means the server default.
func (c *Client) ReplayTrigger(projectID, triggerID string, dryRun bool, limit int) (*ReplayResult, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/projects/" + projectID + "/triggers/" + triggerID + "/replay"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	data, status, err := c.do("POST", path, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("status %d", status)
		}
		return nil, fmt.Errorf("replay failed: %s", e.Error)
	}
	var res ReplayResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
