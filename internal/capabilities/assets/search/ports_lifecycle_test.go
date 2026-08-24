// Package search — ports_lifecycle_test.go locks the canonical
// ACTIVE-only lifecycle filter contract for the MediaReadRepository
// port per SEARCH-T07-LIFECYCLE-DEL (P0, 2026-07-15, Phase 9 cycle 2
// closure of the godlike/06 one-canonical-owner-per-fact invariant
// at the search capability hydration boundary).
//
// What this file pins (5 tests, in 2 layers):
//
// Layer 1 — user-spec semantic surface (4 tests, matches the
// "3 reject + 1 pass-through" spec from SEARCH-T07-LIFECYCLE-DEL):
//  1. TestSearchableLifecycleStates_AcceptsACTIVE — ACTIVE pass-through
//     sanity (the canonical searchable state).
//  2. TestSearchableLifecycleStates_RejectsDELETED — terminal delete
//     state MUST NOT surface in the hydration slice.
//  3. TestSearchableLifecycleStates_RejectsDELETE_REQUESTED — soft
//     delete-pending state MUST NOT surface.
//  4. TestSearchableLifecycleStates_RejectsDRIVE_DELETE_PENDING —
//     drive-side in-flight delete state MUST NOT surface.
//
// Layer 2 — drift-prevention lock (1 test, forward-pointer to
// godlike/07 zero-legacy):
//  5. TestMediaReadRepository_GetMany_SignatureNoAllowStates —
//     reflection-based test on the interface signature. Locks
//     that GetMany takes exactly 3 params (ctx, Actor, assetIDs)
//     and no `allowStates`. Prevents future re-exposure of the
//     parameter that SEARCH-T07-LIFECYCLE-DEL removed.
//
// (Earlier iteration included a `TestSearchableLifecycleStates_MustBeExactlyACTIVE`
//
//	exact-value pin via reflect.DeepEqual. Removed per code-reviewer
//	feedback: that lock was over-spec'd — a future legitimate
//	extension (e.g. surfacing "STAGING" rows) would fail without
//	indicating a contract violation. The 4 semantic tests + the
//	signature lock are the durable contract surface.)
//
// godlike/06 SSOT (one-canonical-owner-per-fact): the constant
// `search.SearchableLifecycleStates` is the SOLE canonical allowlist
// surface for the search capability. The interface
// `MediaReadRepository.GetMany` exposes NO `allowStates` parameter
// — the production impl hardcodes the constant at the call site
// (see internal/app/adapters_media_search.go). Drift in either
// surface (constant adds a delete-* state; interface re-exposes the
// parameter) surfaces as one of these tests failing.
//
// godlike/07 no-fake-availability: the test names are descriptive
// of the contract being pinned, not the implementation. A future
// agent who re-exposes `allowStates` on the interface will see
// `TestMediaReadRepository_GetMany_SignatureNoAllowStates` fail
// at the reflection check. A future agent who adds "DELETED" to
// the constant will see `TestSearchableLifecycleStates_RejectsDELETED`
// fail because the slice now contains the forbidden state.
//
// Related contracts locked by sibling tests:
//   - LifecycleState.IsValidTransition — internal/kernel/asset/lifecycle_state_test.go
//   - LifecycleState.Valid — internal/kernel/asset/lifecycle_test.go
package search

import (
	"reflect"
	"slices"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TestSearchableLifecycleStates_AcceptsACTIVE pins the canonical
// pass-through sanity: ACTIVE is the ONE state that survives
// hydration. The semantic backend in search_backend_semantic.go
// relies on this invariant — if ACTIVE were not in the allowlist,
// every search request would return zero candidates.
//
// This test is the "1 pass-through" of the user-spec's
// "3 reject + 1 pass-through" surface.
func TestSearchableLifecycleStates_AcceptsACTIVE(t *testing.T) {
	if !slices.Contains(SearchableLifecycleStates, string(asset.StateActive)) {
		t.Fatalf("SearchableLifecycleStates MUST permit ACTIVE; got %v",
			SearchableLifecycleStates)
	}
}

// TestSearchableLifecycleStates_AcceptsPUBLISHED pins that the
// stock pipeline's PUBLISHED lifecycle state is searchable. Stock
// assets are indexed with lifecycle_state=PUBLISHED (not ACTIVE);
// without this state in the allowlist, all stock search results
// would be silently dropped during SQLite hydration.
func TestSearchableLifecycleStates_AcceptsPUBLISHED(t *testing.T) {
	if !slices.Contains(SearchableLifecycleStates, "PUBLISHED") {
		t.Fatalf("SearchableLifecycleStates MUST permit PUBLISHED; got %v",
			SearchableLifecycleStates)
	}
}

// TestSearchableLifecycleStates_RejectsDELETED pins the canonical
// rejection of the terminal delete state. DELETED is the
// irreversible terminal state (the row is gone from the user-facing
// projection). If a DELETED row reaches the semantic backend, the
// /internal/v1/media/search response would surface a tombstoned
// asset — exactly the godlike/07 fake-availability class.
func TestSearchableLifecycleStates_RejectsDELETED(t *testing.T) {
	if slices.Contains(SearchableLifecycleStates, string(asset.StateDeleted)) {
		t.Fatalf("SearchableLifecycleStates MUST reject DELETED; got %v",
			SearchableLifecycleStates)
	}
}

// TestSearchableLifecycleStates_RejectsDELETE_REQUESTED pins the
// canonical rejection of the soft-delete-pending state. The
// DELETE_REQUESTED → DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING
// → DELETED state machine is owned by the lifecycle subsystem
// (internal/kernel/asset/lifecycle_state.go); the search
// capability MUST NOT surface any row in the middle of that
// chain — clients must see a stable "gone" experience during
// the async delete pipeline.
//
// This is the "case P0 #2" from the
// architecture/current.yaml#SEARCH-T07-LIFECYCLE-DEL wave-tracker
// (the forward-pointer to lifecycle_state_test.go::IsValidTransition
// pins the state machine itself; this test pins the search-side
// filter on top of that machine).
func TestSearchableLifecycleStates_RejectsDELETE_REQUESTED(t *testing.T) {
	if slices.Contains(SearchableLifecycleStates, string(asset.StateDeleteRequested)) {
		t.Fatalf("SearchableLifecycleStates MUST reject DELETE_REQUESTED; got %v",
			SearchableLifecycleStates)
	}
}

// TestSearchableLifecycleStates_RejectsDRIVE_DELETE_PENDING pins
// the canonical rejection of the drive-side in-flight delete
// state. DRIVE_DELETE_PENDING means the row is logically deleted
// from the user's perspective (the file is being removed from
// Drive) but the lifecycle state machine has not yet progressed
// to INDEX_DELETE_PENDING. The search capability MUST NOT
// surface this row — clients should see the asset as "gone" the
// moment the user requests deletion, not after the entire
// projection pipeline completes.
//
// This is the "case P0 #3" from the wave-tracker; the drive-side
// field name is asserted as `DRIVE_DELETE_PENDING` (not
// `DRIVE_DELETED` — that is a different state further down the
// chain, equally rejected but tested transitively via the
// DELETED + DELETE_REQUESTED assertions).
func TestSearchableLifecycleStates_RejectsDRIVE_DELETE_PENDING(t *testing.T) {
	if slices.Contains(SearchableLifecycleStates, string(asset.StateDriveDeletePending)) {
		t.Fatalf("SearchableLifecycleStates MUST reject DRIVE_DELETE_PENDING; got %v",
			SearchableLifecycleStates)
	}
}

// TestMediaReadRepository_GetMany_SignatureNoAllowStates is the
// reflection-based signature lock. The interface
// `MediaReadRepository.GetMany` was deliberately stripped of the
// `allowStates []string` parameter at SEARCH-T07-LIFECYCLE-DEL
// (P0, 2026-07-15) so the canonical ACTIVE-only filter is the
// SOLE surface (the production impl hardcodes the SSOT constant
// at the call site, not the caller). This test pins the
// post-PR signature so a future agent cannot re-introduce the
// parameter without flipping the constant visibility back to
// the caller — exactly the drift class the PR was designed to
// prevent.
//
// godlike/07 zero-legacy: the reflection check is the durable
// forward-prevention gate. A future `allowStates []string`
// addition to the interface signature surfaces as a failure on
// NumIn() == 3 (or on the In(2) type assertion for []string
// being non-allowStates).
//
// Implementation note (prior bug fix): the reflect.TypeOf
// expression for an interface value returns the *pointer-to-interface*
// type, not the interface type itself. We MUST call .Elem() to
// dereference into the interface type so MethodByName can find
// the GetMany method. Without .Elem(), the test reports
// "method not found" — a regression that previous agent
// versions of this test hit during the first iteration.
//
// Param 1 (Actor) is checked via AssignableTo (structural
// type compatibility) rather than In(1).String() (stringly-typed
// package path). AssignableTo survives renames of the Actor
// type within the same package (e.g. Actor → SearchActor) and
// is the canonical idiomatic check per the existing codebase
// pattern (see internal/application/assets/search/ports_test.go
// for prior art on AssignableTo for port assertions).
func TestMediaReadRepository_GetMany_SignatureNoAllowStates(t *testing.T) {
	t.Helper()
	// Get the interface type, NOT the pointer-to-interface type.
	// reflect.TypeOf((*MediaReadRepository)(nil)) returns *MediaReadRepository
	// (the pointer to the interface value); .Elem() dereferences to
	// the MediaReadRepository interface type itself.
	iface := reflect.TypeOf((*MediaReadRepository)(nil)).Elem()
	if iface == nil {
		t.Fatal("MediaReadRepository interface type is nil after .Elem()")
	}
	if iface.Kind() != reflect.Interface {
		t.Fatalf("MediaReadRepository .Elem() must be an interface; got %s", iface.Kind())
	}
	method, ok := iface.MethodByName("GetMany")
	if !ok {
		t.Fatal("MediaReadRepository.GetMany method not found on interface type")
	}

	// Must take exactly 3 params: ctx, Actor, assetIDs.
	// Param 0 = ctx, Param 1 = Actor, Param 2 = []string (NOT []string allowStates)
	if method.Type.NumIn() != 3 {
		t.Fatalf("MediaReadRepository.GetMany MUST take exactly 3 params (ctx, Actor, assetIDs); got %d params",
			method.Type.NumIn())
	}

	// Param 0: context.Context (string check is stable — context
	// is the stdlib canonical type, no rename risk).
	if got := method.Type.In(0).String(); got != "context.Context" {
		t.Fatalf("GetMany param 0 MUST be context.Context; got %s", got)
	}

	// Param 1: search.Actor — checked via AssignableTo so the
	// assertion survives future renames of the Actor type
	// within the search package (e.g. Actor → SearchActor).
	actorType := reflect.TypeOf(Actor{})
	if !method.Type.In(1).AssignableTo(actorType) && !actorType.AssignableTo(method.Type.In(1)) {
		t.Fatalf("GetMany param 1 MUST be assignable to search.Actor; got %s", method.Type.In(1).String())
	}

	// Param 2: []string (the assetIDs slice — NOT an allowStates filter).
	// The interface MUST NOT take a 4th param for allowStates; the
	// constant search.SearchableLifecycleStates is the SSOT and is
	// hardcoded at the impl call site.
	if got := method.Type.In(2).String(); got != "[]string" {
		t.Fatalf("GetMany param 2 MUST be []string (assetIDs); got %s — an allowStates-like filter MUST NOT be re-exposed at the interface boundary", got)
	}
}
