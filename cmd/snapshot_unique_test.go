package cmd

import (
	"encoding/json"
	"testing"
)

func TestApplyResultReadsUniqueConstraints(t *testing.T) {
	raw := `{"status":"partial","unique_constraints":[{"name":"users(email)","action":"skipped","status":"conflict",
		"error":{"code":"duplicate_values","message":"existing records already share values"}}]}`
	var res applyResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.UniqueConstraints) != 1 || res.UniqueConstraints[0].Name != "users(email)" {
		t.Fatalf("unique_constraints not read: %+v", res.UniqueConstraints)
	}
}
