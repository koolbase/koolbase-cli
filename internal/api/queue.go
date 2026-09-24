package api

import (
	"encoding/json"
	"fmt"
)

type QueueJob struct {
	ID          string                 `json:"id"`
	Function    string                 `json:"function"`
	Payload     map[string]interface{} `json:"payload"`
	Attempt     int                    `json:"attempt"`
	MaxAttempts int                    `json:"max_attempts"`
	RunAt       string                 `json:"run_at"`
	LastError   *string                `json:"last_error"`
	CreatedAt   string                 `json:"created_at"`
}

func queueError(action string, data []byte, status int) error {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &e)
	if e.Error == "" {
		e.Error = fmt.Sprintf("status %d", status)
	}
	return fmt.Errorf("%s failed: %s", action, e.Error)
}

func (c *Client) EnqueueJob(projectID, function string, payload map[string]interface{}, delaySeconds int) (*QueueJob, error) {
	data, status, err := c.do("POST", "/v1/projects/"+projectID+"/queue/jobs", map[string]interface{}{
		"function": function, "payload": payload, "delay_seconds": delaySeconds,
	})
	if err != nil {
		return nil, err
	}
	if status != 201 {
		return nil, queueError("queueing the job", data, status)
	}
	var out QueueJob
	return &out, json.Unmarshal(data, &out)
}

func (c *Client) ListQueueJobs(projectID string) ([]QueueJob, error) {
	data, status, err := c.do("GET", "/v1/projects/"+projectID+"/queue/jobs", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, queueError("listing queued jobs", data, status)
	}
	var out []QueueJob
	return out, json.Unmarshal(data, &out)
}

func (c *Client) CancelQueueJob(projectID, jobID string) error {
	data, status, err := c.do("DELETE", "/v1/projects/"+projectID+"/queue/jobs/"+jobID, nil)
	if err != nil {
		return err
	}
	if status != 204 && status != 200 {
		return queueError("cancelling the job", data, status)
	}
	return nil
}
