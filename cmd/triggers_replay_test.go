package cmd

import (
	"strings"
	"testing"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
)

func TestFormatReplayDryRun(t *testing.T) {
	out := formatReplayResult(&api.ReplayResult{DryRun: true, Matching: 24, Queued: 24, Collection: "enterprise_metrics",
		FunctionName: "classify-enterprise", EventType: "db.record.created", EstimatedSeconds: 30})
	for _, want := range []string{"Dry run: would queue 24 of 24 records in enterprise_metrics for classify-enterprise (db.record.created).", "About 30s"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "payload.replay") {
		t.Errorf("dry run should not describe deliveries:\n%s", out)
	}
}

func TestFormatReplayRealWithRemaining(t *testing.T) {
	out := formatReplayResult(&api.ReplayResult{Matching: 1500, Queued: 1000, Remaining: 500, Collection: "c",
		FunctionName: "f", EventType: "db.record.updated", EstimatedSeconds: 1200})
	for _, want := range []string{"Queued 1000 of 1500", "500 more records were beyond --limit", "payload.replay = true"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}
