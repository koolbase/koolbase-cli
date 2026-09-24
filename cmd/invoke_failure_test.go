package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
)

func TestFormatInvokeFailureDatabaseError(t *testing.T) {
	out := formatInvokeFailure(&api.InvokeResponse{
		Status:          500,
		Error:           "invalid request body",
		LogID:           "log_1",
		ErrorStructured: json.RawMessage(`{"type":"KoolbaseDatabaseException","message":"invalid request body","status":400,"operation":"update","record_id":"rec_9"}`),
	})
	for _, want := range []string{
		"Function failed (status 500)",
		"ctx.db.update failed with HTTP 400: invalid request body",
		"record: rec_9",
		"Log ID: log_1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatInvokeFailurePlainError(t *testing.T) {
	out := formatInvokeFailure(&api.InvokeResponse{
		Status:          500,
		Error:           "boom",
		ErrorStructured: json.RawMessage(`{"type":"TypeError","message":"boom"}`),
	})
	if !strings.Contains(out, "Error (TypeError): boom") || strings.Contains(out, "ctx.db") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestFormatInvokeFailureNoStructuredError(t *testing.T) {
	out := formatInvokeFailure(&api.InvokeResponse{Error: "function not found"})
	if !strings.Contains(out, " Error: function not found") {
		t.Errorf("unexpected output:\n%s", out)
	}
}
