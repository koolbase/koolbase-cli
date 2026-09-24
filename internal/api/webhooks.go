package api

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type Webhook struct {
	ID         string   `json:"id"`
	ProjectID  string   `json:"project_id"`
	URL        string   `json:"url"`
	Collection string   `json:"collection"`
	Events     []string `json:"events"`
	Enabled    bool     `json:"enabled"`
	CreatedAt  string   `json:"created_at"`
}

type WebhookDelivery struct {
	ID             string  `json:"id"`
	WebhookID      string  `json:"webhook_id"`
	EventType      string  `json:"event_type"`
	EventID        string  `json:"event_id"`
	Status         string  `json:"status"`
	Attempt        int     `json:"attempt"`
	MaxAttempts    int     `json:"max_attempts"`
	NextAttemptAt  string  `json:"next_attempt_at"`
	LastStatusCode *int    `json:"last_status_code"`
	LastError      *string `json:"last_error"`
	CreatedAt      string  `json:"created_at"`
	DeliveredAt    *string `json:"delivered_at"`
}

// CreatedWebhook carries the signing secret, which the server returns only once.
type CreatedWebhook struct {
	Webhook Webhook `json:"webhook"`
	Secret  string  `json:"secret"`
}

func webhookError(action string, data []byte, status int) error {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &e)
	if e.Error == "" {
		e.Error = fmt.Sprintf("status %d", status)
	}
	return fmt.Errorf("%s failed: %s", action, e.Error)
}

func (c *Client) ListWebhooks(projectID string) ([]Webhook, error) {
	data, status, err := c.do("GET", "/v1/projects/"+projectID+"/webhooks", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, webhookError("listing webhooks", data, status)
	}
	var out []Webhook
	return out, json.Unmarshal(data, &out)
}

func (c *Client) CreateWebhook(projectID, url, collection string, events []string) (*CreatedWebhook, error) {
	data, status, err := c.do("POST", "/v1/projects/"+projectID+"/webhooks", map[string]interface{}{
		"url": url, "collection": collection, "events": events,
	})
	if err != nil {
		return nil, err
	}
	if status != 201 {
		return nil, webhookError("creating the webhook", data, status)
	}
	var out CreatedWebhook
	return &out, json.Unmarshal(data, &out)
}

func (c *Client) DeleteWebhook(projectID, id string) error {
	data, status, err := c.do("DELETE", "/v1/projects/"+projectID+"/webhooks/"+id, nil)
	if err != nil {
		return err
	}
	if status != 204 && status != 200 {
		return webhookError("deleting the webhook", data, status)
	}
	return nil
}

func (c *Client) ListWebhookDeliveries(projectID, id string, limit int) ([]WebhookDelivery, error) {
	path := "/v1/projects/" + projectID + "/webhooks/" + id + "/deliveries"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	data, status, err := c.do("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, webhookError("listing deliveries", data, status)
	}
	var out []WebhookDelivery
	return out, json.Unmarshal(data, &out)
}

func (c *Client) RedeliverWebhook(projectID, deliveryID string) error {
	data, status, err := c.do("POST", "/v1/projects/"+projectID+"/webhooks/deliveries/"+deliveryID+"/redeliver", nil)
	if err != nil {
		return err
	}
	if status != 200 {
		return webhookError("redelivering", data, status)
	}
	return nil
}
