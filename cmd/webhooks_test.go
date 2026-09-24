package cmd

import (
	"strings"
	"testing"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
)

func TestResolveWebhookEvents(t *testing.T) {
	got := strings.Join(resolveWebhookEvents([]string{"insert,update", "delete", "db.record.created"}), ",")
	if got != "db.record.created,db.record.updated,db.record.deleted" {
		t.Errorf("events = %s", got)
	}
}

func TestFormatCreatedWebhookShowsSecretWithWarning(t *testing.T) {
	out := formatCreatedWebhook(&api.CreatedWebhook{
		Webhook: api.Webhook{ID: "wh1", URL: "https://x.example/h", Collection: "orders", Events: []string{"db.record.created"}},
		Secret:  "whsec_abc",
	})
	for _, want := range []string{"Webhook created: wh1", "whsec_abc", "shown once", "X-Koolbase-Signature"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestFormatWebhooksAndDeliveries(t *testing.T) {
	if !strings.Contains(formatWebhooks(nil), "No webhooks") {
		t.Error("empty list should say so")
	}
	list := formatWebhooks([]api.Webhook{{ID: "wh1", URL: "https://x.example/h", Collection: "orders", Events: []string{"db.record.created"}, Enabled: true}})
	if !strings.Contains(list, "wh1") || !strings.Contains(list, "https://x.example/h") || strings.Contains(list, "whsec_") {
		t.Errorf("list = %s", list)
	}
	code := 404
	msg := "receiver answered HTTP 404"
	d := formatDeliveries([]api.WebhookDelivery{{ID: "d1", EventType: "db.record.created", Status: "pending", Attempt: 1, MaxAttempts: 6, LastStatusCode: &code, LastError: &msg}})
	for _, want := range []string{"d1", "pending", "1/6", "404", "receiver answered"} {
		if !strings.Contains(d, want) {
			t.Errorf("deliveries missing %q:\n%s", want, d)
		}
	}
	if !strings.Contains(formatDeliveries(nil), "No deliveries") {
		t.Error("empty deliveries should say so")
	}
}
