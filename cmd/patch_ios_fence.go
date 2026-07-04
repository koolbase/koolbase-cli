package cmd

import (
	"bytes"
	"fmt"
)

// fenceUnsafePatterns scans a compiled dart2bytecode payload for constructs
// that are known to crash on-device at patch APPLY time (silent hard fault in
// the VM's BytecodeLoader, before the override is installed). These are the
// two load-time member-resolution gaps diagnosed Jul 2026:
//
//  1. SUSPENDING ASYNC: a patched body containing `await` emits a DirectCall to
//     dart:async::_SuspendState::_await. Resolving that target during a
//     standalone patch load faults. (Plain / sync-completing async — which has
//     _initAsync/_returnAsync but NOT _await — is proven safe and must NOT be
//     fenced.)
//
//  2. MEMBER ACCESS (field getters/setters): a patched body reading/writing
//     `this.field` (or any property) emits an InterfaceCall to a get:/set:
//     accessor namespaced to the patch library, which has no such member in
//     hoisted patch-mode. Resolving it faults.
//
// Detection is a substring scan of the bytecode's string table (target names
// are stored as readable UTF-8). This is intentionally conservative: it may
// reject a patch that would in fact apply, but it will never PASS a patch that
// crashes on-device. A false reject is a clear build-time error; a false pass
// is a user's app crashing in production. We choose the safe failure.
//
// Returns nil if the patch is safe to build, or a descriptive error naming the
// unsupported construct.
func fenceUnsafePatterns(bytecode []byte) error {
	// Rule 1: suspending async. Signal is the _await helper specifically,
	// NOT _SuspendState in general (plain async carries _initAsync etc. and
	// is safe).
	if bytes.Contains(bytecode, []byte("_await")) {
		return fmt.Errorf(
			"patch contains a suspending async function (uses `await`), " +
				"which is not yet supported by iOS code push and would crash " +
				"on apply. Rework the change to avoid `await` in the patched " +
				"function, or wait for suspend-async support")
	}

	// Rule 2: field / property access via getter or setter. Signal is a
	// get:/set: accessor name in the string table.
	if bytes.Contains(bytecode, []byte("get:")) || bytes.Contains(bytecode, []byte("set:")) {
		return fmt.Errorf(
			"patch contains a function that reads or writes an object field " +
				"or property (get:/set: access), which is not yet supported by " +
				"iOS code push and would crash on apply. Rework the change to " +
				"avoid touching field/property state in the patched function, " +
				"or wait for member-access support")
	}

	return nil
}
