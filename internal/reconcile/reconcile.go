// Package reconcile decides what to do with each file when Koolbase
// regenerates a project that someone has been working in.
//
// Pure on purpose: it reads no files and writes none. The CLI hands it
// the local tree, the GitHub adapter hands it a repository tree, and
// both get the same answer. Reconciliation rules living in two places
// is how the two destinations drift, and the cost of that drift is
// somebody's afternoon of work.
//
// THE INVARIANT, above everything else in this package: Koolbase never
// overwrites, deletes, or claims to have synchronised a file the
// developer modified. Every rule below serves that.
package reconcile

import "sort"

// Action is what happens to one path.
type Action string

const (
	// Add: the export has it, the destination does not.
	Add Action = "add"

	// Replace: the destination has it, unchanged since Koolbase last
	// wrote it, and the export has new content.
	Replace Action = "replace"

	// Delete: Koolbase wrote it before, the export no longer has it --
	// a screen that was removed in the Designer -- and nobody has
	// touched it since.
	Delete Action = "delete"

	// Conflict: the developer's file. Never written, never removed.
	Conflict Action = "conflict"
)

// Why a path conflicted. The developer reads this, so it says what
// happened rather than what the code decided.
type Reason string

const (
	// The path exists but Koolbase never wrote it: a file of theirs
	// standing where a generated one wants to go.
	ReasonUnowned Reason = "a file of yours is already there"

	// Koolbase wrote it, and it has changed since.
	ReasonModified Reason = "you have changed this since it was generated"

	// Koolbase wrote it, the export dropped it, and it has changed --
	// so it cannot simply be removed.
	ReasonModifiedThenRemoved Reason = "you have changed this, and the design no longer generates it"
)

// Change is one path's outcome.
type Change struct {
	Path   string
	Action Action

	// Content for an Add or a Replace. Empty otherwise.
	Content string

	// Set only on a Conflict.
	Reason Reason
}

// Plan is the whole reconciliation, and the only thing a caller acts on.
type Plan struct {
	Changes []Change

	// The manifest to record AFTER the plan is applied. Deliberately
	// only meaningful when Conflicts is empty: recording a new hash for
	// a file that was skipped would tell the NEXT reconciliation that
	// the developer's version is Koolbase's, and it would be silently
	// overwritten. That bug would be invisible until it cost someone
	// their work.
	NextManifest map[string]string
}

// Conflicts returns the changes that cannot be applied.
func (p Plan) Conflicts() []Change {
	var out []Change
	for _, c := range p.Changes {
		if c.Action == Conflict {
			out = append(out, c)
		}
	}
	return out
}

// Reconcile compares three things: what Koolbase wrote last time
// (previous), what is there now (destination), and what it would write
// today (export).
//
// Three inputs rather than two is what makes deletion safe. With only
// "current" and "new" there is no way to tell a file the developer
// wrote from one Koolbase wrote and the design has since dropped.
//
// previous and destination map path to sha256; export maps path to
// content.
func Reconcile(
	previous map[string]string,
	destination map[string]string,
	export map[string]string,
	hash func(string) string,
) Plan {
	plan := Plan{NextManifest: map[string]string{}}

	paths := map[string]bool{}
	for p := range export {
		paths[p] = true
	}
	for p := range previous {
		paths[p] = true
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	// Sorted so a plan reads the same twice and a test can rely on it.
	sort.Strings(ordered)

	for _, path := range ordered {
		content, inExport := export[path]
		was, koolbaseWroteIt := previous[path]
		now, onDisk := destination[path]

		switch {
		case inExport && !onDisk:
			// Nothing there. Whether Koolbase wrote it before does not
			// matter: the developer may have deleted it, and putting it
			// back is what regenerating means.
			plan.Changes = append(plan.Changes, Change{
				Path: path, Action: Add, Content: content,
			})
			plan.NextManifest[path] = hash(content)

		case inExport && !koolbaseWroteIt:
			// Something is there that Koolbase never wrote.
			plan.Changes = append(plan.Changes, Change{
				Path: path, Action: Conflict, Reason: ReasonUnowned,
			})

		case inExport && now != was:
			plan.Changes = append(plan.Changes, Change{
				Path: path, Action: Conflict, Reason: ReasonModified,
			})

		case inExport:
			// Ours, untouched. Replace even when the content is
			// identical -- the caller can skip a no-op write, but the
			// plan states the intent.
			plan.Changes = append(plan.Changes, Change{
				Path: path, Action: Replace, Content: content,
			})
			plan.NextManifest[path] = hash(content)

		case koolbaseWroteIt && onDisk && now == was:
			// Dropped from the design, untouched since. Removing it is
			// what keeps a deleted screen from living forever.
			plan.Changes = append(plan.Changes, Change{
				Path: path, Action: Delete,
			})

		case koolbaseWroteIt && onDisk:
			plan.Changes = append(plan.Changes, Change{
				Path:   path,
				Action: Conflict,
				Reason: ReasonModifiedThenRemoved,
			})

			// koolbaseWroteIt && !onDisk: already gone. Nothing to do,
			// and it leaves the manifest, which is correct.
		}
	}

	return plan
}
