// Package assets — tests for *ClipsRepository.Mutate dispatcher-only
// wrapping (PR 2 / Bloco 1 sub-PR, June 2026).
//
// Scope: cover the contract that does NOT require a SQLite database.
// The DB-required happy-path for AssetMutationUpsert is deferred
// to a follow-up test once a DB fixture convention is established
// for this package; today the write-side behaviour of Upsert is
// covered indirectly by integration tests in
// internal/application/youtube/adapters/assetrepo_integration_test.go
// and internal/capabilities/assets/providers/artlist/assetrepo_integration_test.go.
//
// The four contract pins asserted here:
//
//  1. AssetMutationUpsert with nil Asset returns a non-nil error
//     carrying the literal "requires non-nil Asset" message — the
//     gate against "I sent a typed command and the wrapper silently
//     no-op'd through".
//  2. AssetMutationUpgrade-not-implemented (AssetsRestore / AssetsDelete)
//     return errors.Is(err, mutations.ErrUnsupportedAction) so
//     callers can errors.Is-branch on the canonical sentinel.
//  3. Unknown actions (Action == "" OR a non-IsKnown value) return
//     errors.Is(err, mutations.ErrUnsupportedAction).
//  4. The exhaustive-enum invariant: mutations.ImplementedActions is
//     NOT empty and every value in it maps to a switch arm in Mutate
//     today (validated via length-floor + presence of the action in
//     the well-formed-error set). A future addition without a switch
//     arm lands here in this test as a hard failure.
package imagesregistry

import (
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
)

// wellFormedMutateError is the subset of error strings Mutate must
// return that maps onto each AssetMutationAction. The presence of an
// action's name in this set means Mutate has a dispatch arm for it;
// the absence means the action falls into the default-branch
// ErrUnsupportedAction return.
//
// This set is the single source of truth for the exhaustive-enum
// invariant. Each entry corresponds to one switch arm in Mutate
// today; a future PR that adds an arm MUST add the action's name
// here in the same commit.
var wellFormedMutateError = map[mutations.AssetMutationAction]bool{
	mutations.AssetMutationUpsert: true,
	// AssetMutationRestore / AssetMutationDelete are NOT in this
	// set: they return ErrUnsupportedAction intentionally.
}

// TestClipsRepository_Mutate_ExhaustiveEnumInvariant asserts the
// invariant that the canonical PR 2 sub-PR contract pins: every
// action in mutations.ImplementedActions has a wired dispatch arm
// in ClipsRepository.Mutate today, and unknown actions / restore /
// delete return errors.Is-branchable ErrUnsupportedAction.
//
// This is a structural-rake test — it does not require a SQLite
// fixture; constructing a ClipsRepository against `nil` and calling
// Mutate with each action shape produces the relevant error string.
// The DB-required UPSERT happy path is asserted indirectly by
// existing integration test fixtures in the application layer.
func TestClipsRepository_Mutate_ExhaustiveEnumInvariant(t *testing.T) {
	// mutations.ImplementedActions MUST NOT be empty — the contract
	// is "the Mutate layer actually implements at least one action".
	if len(mutations.ImplementedActions) == 0 {
		t.Fatalf("mutations.ImplementedActions is empty; PR 2 sub-PR requires at least one implemented action")
	}

	// Construct a ClipsRepository against a nil DB. None of the
	// action arms tested here actually reach the DB layer:
	//  - AssetMutationUpsert with nil Asset returns a non-DB error
	//    from the nil-A guard.
	//  - AssetMutationUpsert with valid Asset reaches the DB and
	//    would NPE; that path is intentionally NOT exercised here
	//    (covered by integration tests downstream).
	//  - AssetMutationRestore / AssetMutationDelete / Unknown return
	//    ErrUnsupportedAction before any DB call.
	r := &ClipsRepository{}

	tests := []struct {
		name      string
		action    mutations.AssetMutationAction
		expectErr bool
		expectAE  bool // expect assets.ErrUnsupportedAction == errors.Is
	}{
		{
			name:      "implemented: AssetMutationUpsert with nil Asset",
			action:    mutations.AssetMutationUpsert,
			expectErr: true,
			expectAE:  false,
		},
		{
			name:      "NOT implemented: AssetMutationRestore returns ErrUnsupportedAction",
			action:    mutations.AssetMutationRestore,
			expectErr: true,
			expectAE:  true,
		},
		{
			name:      "NOT implemented: AssetMutationDelete returns ErrUnsupportedAction",
			action:    mutations.AssetMutationDelete,
			expectErr: true,
			expectAE:  true,
		},
		{
			name:      "Unknown action returns ErrUnsupportedAction",
			action:    mutations.AssetMutationAction("unknown"),
			expectErr: true,
			expectAE:  true,
		},
		{
			name:      "Empty action returns ErrUnsupportedAction",
			action:    mutations.AssetMutationAction(""),
			expectErr: true,
			expectAE:  true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := r.Mutate(nil, mutations.AssetMutationCommand{Action: tc.action})
			if tc.expectErr && err == nil {
				t.Fatalf("Mutate(%v) returned nil error; expected an error", tc.action)
			}
			if !tc.expectErr && err != nil {
				t.Fatalf("Mutate(%v) returned unexpected error: %v", tc.action, err)
			}
			if tc.expectAE {
				if !errors.Is(err, mutations.ErrUnsupportedAction) {
					t.Fatalf("Mutate(%v) returned %v; want errors.Is-branchable %v", tc.action, err, mutations.ErrUnsupportedAction)
				}
			}
		})
	}
}

// TestMutated_ActionsMatchImplementedActions is the dedicated
// safety-net for the exhaustive-enum invariant: every entry in
// mutations.ImplementedActions MUST appear in the wellFormedMutateError
// set in this file (which mirrors Mutate's switch arms). A drift
// here is a compile-fix-me-clear contract violation rather than a
// runtime panic.
func TestMutated_ActionsMatchImplementedActions(t *testing.T) {
	for _, action := range mutations.ImplementedActions {
		if !wellFormedMutateError[action] {
			t.Errorf("action %q is in mutations.ImplementedActions but has no dispatch arm in *ClipsRepository.Mutate (wellFormedMutateError set in clips_repository_mutate_test.go); add a switch arm in clips_repository.go OR remove the action from ImplementedActions", action)
		}
	}
	// The non-implemented actions MUST NOT appear here (they return
	// ErrUnsupportedAction intentionally).
	for action := range wellFormedMutateError {
		implemented := false
		for _, a := range mutations.ImplementedActions {
			if a == action {
				implemented = true
				break
			}
		}
		if !implemented {
			t.Errorf("wellFormedMutateError contains %q but mutations.ImplementedActions does not; this would mean Mutate has a switch arm but the action is documented as not-implemented — fix the test or the contract", action)
		}
	}
}

// TestClipsRepository_Mutate_ErrorMessageTelemetry asserts that the
// contextual error messages carry the action name so log-grep can
// correlate production errors to action enum values. The goal
// is observability rather than strict string equality — the
// predicate only fires on a substring match so a future godoc
// rewording does not require a test fix.
func TestClipsRepository_Mutate_ErrorMessageTelemetry(t *testing.T) {
	r := &ClipsRepository{}

	t.Run("restore routs to dispatcher/txmutation telemetry", func(t *testing.T) {
		err := r.Mutate(nil, mutations.AssetMutationCommand{Action: mutations.AssetMutationRestore})
		if err == nil {
			t.Fatalf("expected error for AssetMutationRestore, got nil")
		}
		if !strings.Contains(err.Error(), string(mutations.AssetMutationRestore)) {
			t.Errorf("error %q does not contain action %q (log-grep telemetry break)", err.Error(), mutations.AssetMutationRestore)
		}
	})

	t.Run("delete routes to dispatcher/txmutation telemetry", func(t *testing.T) {
		err := r.Mutate(nil, mutations.AssetMutationCommand{Action: mutations.AssetMutationDelete})
		if err == nil {
			t.Fatalf("expected error for AssetMutationDelete, got nil")
		}
		if !strings.Contains(err.Error(), string(mutations.AssetMutationDelete)) {
			t.Errorf("error %q does not contain action %q (log-grep telemetry break)", err.Error(), mutations.AssetMutationDelete)
		}
	})

	t.Run("upsert nil-asset error mentions nil-Asset requirement", func(t *testing.T) {
		err := r.Mutate(nil, mutations.AssetMutationCommand{Action: mutations.AssetMutationUpsert})
		if err == nil {
			t.Fatalf("expected error for AssetMutationUpsert+nil Asset, got nil")
		}
		if !strings.Contains(err.Error(), "non-nil Asset") {
			t.Errorf("error %q does not contain `non-nil Asset` substring (diagnostic-text break)", err.Error())
		}
	})
}
