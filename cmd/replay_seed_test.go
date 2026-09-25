package cmd

import "testing"

// Seeded records don't fire triggers, so seed apply says so and points to
// triggers replay, which must stay registered with the flags it documents.
func TestTriggersReplayAndSeedNote(t *testing.T) {
	found := false
	for _, c := range triggersCmd.Commands() {
		if c == triggersReplayCmd {
			found = true
		}
	}
	if !found {
		t.Error("triggers replay is not registered under triggers")
	}
	for _, f := range []string{
		"project",
		"dry-run",
		"limit",
		"project",
		"dry-run",
		"limit",
	} {
		if triggersReplayCmd.Flags().Lookup(f) == nil {
			t.Errorf("triggers replay: missing --%s", f)
		}
	}
	if seedApplyCmd.PostRun == nil {
		t.Error("seed apply should point to triggers replay after a successful apply")
	}
}
