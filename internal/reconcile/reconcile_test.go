package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func h(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// actionFor finds one path's outcome, so a test says what it means
// rather than indexing into a slice.
func actionFor(p Plan, path string) (Change, bool) {
	for _, c := range p.Changes {
		if c.Path == path {
			return c, true
		}
	}
	return Change{}, false
}

func TestAddsWhatIsNotThere(t *testing.T) {
	p := Reconcile(nil, nil, map[string]string{"a.dart": "one"}, h)
	c, ok := actionFor(p, "a.dart")
	if !ok || c.Action != Add || c.Content != "one" {
		t.Fatalf("want add with content, got %+v", c)
	}
	if p.NextManifest["a.dart"] != h("one") {
		t.Fatal("an added file must be recorded")
	}
}

func TestReplacesItsOwnUntouchedFile(t *testing.T) {
	p := Reconcile(
		map[string]string{"a.dart": h("old")},
		map[string]string{"a.dart": h("old")},
		map[string]string{"a.dart": "new"},
		h,
	)
	c, _ := actionFor(p, "a.dart")
	if c.Action != Replace {
		t.Fatalf("want replace, got %s", c.Action)
	}
}

func TestRefusesAFileTheDeveloperChanged(t *testing.T) {
	// The case the whole package exists for.
	p := Reconcile(
		map[string]string{"a.dart": h("generated")},
		map[string]string{"a.dart": h("theirs")},
		map[string]string{"a.dart": "regenerated"},
		h,
	)
	c, _ := actionFor(p, "a.dart")
	if c.Action != Conflict || c.Reason != ReasonModified {
		t.Fatalf("want a modified conflict, got %+v", c)
	}
	if c.Content != "" {
		t.Fatal("a conflict must carry no content to write")
	}
	if _, recorded := p.NextManifest["a.dart"]; recorded {
		t.Fatal(
			"a skipped file must NOT advance the manifest -- " +
				"the next run would see a match and overwrite them",
		)
	}
}

func TestRefusesToStandOnAFileItNeverWrote(t *testing.T) {
	p := Reconcile(
		nil,
		map[string]string{"a.dart": h("theirs")},
		map[string]string{"a.dart": "ours"},
		h,
	)
	c, _ := actionFor(p, "a.dart")
	if c.Action != Conflict || c.Reason != ReasonUnowned {
		t.Fatalf("want an unowned conflict, got %+v", c)
	}
}

func TestDeletesWhatTheDesignDropped(t *testing.T) {
	// A screen deleted in the Designer. Its file must not live forever.
	p := Reconcile(
		map[string]string{"gone.dart": h("old")},
		map[string]string{"gone.dart": h("old")},
		nil,
		h,
	)
	c, _ := actionFor(p, "gone.dart")
	if c.Action != Delete {
		t.Fatalf("want delete, got %s", c.Action)
	}
	if _, still := p.NextManifest["gone.dart"]; still {
		t.Fatal("a deleted file must leave the manifest")
	}
}

func TestWillNotDeleteWhatTheyChanged(t *testing.T) {
	// Dropped from the design AND edited. Deleting it would destroy
	// work; the developer decides.
	p := Reconcile(
		map[string]string{"gone.dart": h("old")},
		map[string]string{"gone.dart": h("theirs")},
		nil,
		h,
	)
	c, _ := actionFor(p, "gone.dart")
	if c.Action != Conflict || c.Reason != ReasonModifiedThenRemoved {
		t.Fatalf("want a modified-then-removed conflict, got %+v", c)
	}
}

func TestAFileAlreadyGoneIsNotADeletion(t *testing.T) {
	// They deleted it themselves and the design dropped it too. There
	// is nothing to do, and nothing to report.
	p := Reconcile(
		map[string]string{"gone.dart": h("old")},
		nil,
		nil,
		h,
	)
	if len(p.Changes) != 0 {
		t.Fatalf("want no changes, got %+v", p.Changes)
	}
}

func TestReaddsAFileTheyDeleted(t *testing.T) {
	// Still in the design, gone from disk. Regenerating means putting
	// it back.
	p := Reconcile(
		map[string]string{"a.dart": h("old")},
		nil,
		map[string]string{"a.dart": "new"},
		h,
	)
	c, _ := actionFor(p, "a.dart")
	if c.Action != Add {
		t.Fatalf("want add, got %s", c.Action)
	}
}

func TestPlanIsStable(t *testing.T) {
	// Twice over the same input is the same plan, so a diff a developer
	// reads does not reshuffle.
	prev := map[string]string{"b.dart": h("x")}
	dest := map[string]string{"b.dart": h("x")}
	exp := map[string]string{"a.dart": "one", "b.dart": "two", "c.dart": "three"}
	first := Reconcile(prev, dest, exp, h)
	second := Reconcile(prev, dest, exp, h)
	for i := range first.Changes {
		if first.Changes[i].Path != second.Changes[i].Path {
			t.Fatal("plan order is not stable")
		}
	}
}

func TestConflictsAreReported(t *testing.T) {
	p := Reconcile(
		map[string]string{"a.dart": h("gen")},
		map[string]string{"a.dart": h("theirs")},
		map[string]string{"a.dart": "new", "b.dart": "fresh"},
		h,
	)
	if len(p.Conflicts()) != 1 {
		t.Fatalf("want one conflict, got %d", len(p.Conflicts()))
	}
}
