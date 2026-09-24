package cmd

import "testing"

func TestResolveTriggerEvent(t *testing.T) {
	for in, want := range map[string]string{
		"insert":                 "db.record.created",
		"UPDATE":                 "db.record.updated",
		" delete ":               "db.record.deleted",
		"db.record.created":      "db.record.created",
		"storage.object.created": "storage.object.created",
		"auth.user.registered":   "auth.user.registered",
		"made.up":                "made.up",
	} {
		if got := resolveTriggerEvent(in); got != want {
			t.Errorf("resolveTriggerEvent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTriggerNeedsTarget(t *testing.T) {
	for event, want := range map[string]bool{
		"db.record.created":      true,
		"db.vector.set":          true,
		"storage.object.deleted": true,
		"auth.user.registered":   false,
		"auth.password.changed":  false,
	} {
		if got := triggerNeedsTarget(event); got != want {
			t.Errorf("triggerNeedsTarget(%q) = %v, want %v", event, got, want)
		}
	}
}
