package cmd

import (
	"strings"
	"testing"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
)

func TestParsePayload(t *testing.T) {
	if p, err := parsePayload(""); err != nil || len(p) != 0 {
		t.Errorf("empty payload = %v, %v; want {}", p, err)
	}
	if p, err := parsePayload(`{"userId":"u_1"}`); err != nil || p["userId"] != "u_1" {
		t.Errorf("object payload = %v, %v", p, err)
	}
	if _, err := parsePayload(`{nope`); err == nil {
		t.Error("invalid JSON must be refused")
	}
	if _, err := parsePayload(`[1,2]`); err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Errorf("array payload: %v; want a JSON-object error", err)
	}
}

func TestFormatQueueJobs(t *testing.T) {
	if !strings.Contains(formatQueueJobs(nil), "No pending jobs") {
		t.Error("empty list should say so")
	}
	msg := "boom"
	out := formatQueueJobs([]api.QueueJob{{ID: "j1", Function: "send-email", Attempt: 1, MaxAttempts: 5, RunAt: "2026-09-25T10:00:00Z", LastError: &msg}})
	for _, want := range []string{"j1", "send-email", "1/5", "2026-09-25T10:00:00Z", "boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing %q:\n%s", want, out)
		}
	}
}
