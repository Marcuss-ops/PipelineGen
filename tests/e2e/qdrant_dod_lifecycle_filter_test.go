// Package e2e — Qdrant DoD Gate 9 (Lifecycle Filter) hermetic TDD lock
// (QDRANT-DOD-FINAL-2026-07-08, Test 10 + P2, deadline 2026-08-15).
//
// godlike/06 SSOT: the canonical compiler for Qdrant search filters is
// CompileQdrantFilter in
// internal/platform/qdrant/search/filter_compiler.go. Per
// filter_compiler.go §3: "LifecycleState allow-list is ALWAYS present
// (defaults to {"ACTIVE"} when AssetFilter.LifecycleState is empty)."
// The lifecycleClauses function emits one min_should condition per state in
// the allow-list, and the compiled filter carries Qdrant's structured
// `min_should: {conditions: [...], min_count: 1}` object — lifecycle
// states are alternatives (ACTIVE+PUBLISHED must NOT AND together), so
// the canonical placement is the min_should conditions branch, not `must`.
// The default-ACTIVE invariant is the load-bearing contract for Gate 9
// — without it, search results would silently include
// DELETED / DELETE_REQUESTED points.
//
// This test pins the contract in 6 hermetic sub-cases:
//
//	(1) Default empty LifecycleState → filter contains the canonical
//	    lifecycle_state=ACTIVE condition + min_should.min_count=1
//	    (the godlike/06 SSOT default).
//	(2) Caller-supplied LifecycleState=["ACTIVE","PUBLISHED"] → filter
//	    contains BOTH min_should conditions (per lifecycleClauses expansion).

//	(3) Admin override LifecycleState=["DELETED"] → filter contains
//	    DELETED lifecycle condition (for reconcile/snapshot admin paths).
//	(4) WorkspaceID empty + IsSystem=false → returns typed error
//	    (fail-closed per filter_compiler.go §Invariants 1).
//	(5) WorkspaceID="default" + IsSystem=false → returns typed error
//	    (reserved-sentinel rejection per filter_compiler.go §Invariants 1).
//	(6) IsSystem=true + WorkspaceID="" → succeeds (admin reconcile path
//	    bypasses the workspace must-clause).
//
// Each sub-case is hermetic (no live Qdrant, no live PipelineGen —
// pure-function test of the filter compiler). The compiled filter
// shape is the wire body that SearchAdapter.HybridSearch sends to
// Qdrant via transport.Client.HybridSearchPoints; the test locks the
// invariant at the canonical compiler surface so a future regression
// in lifecycleClauses or CompileQdrantFilter surfaces as a test
// failure rather than a silent full-collection scan in production.
//
// godlike/07 NO-FAKE-AVAILABILITY: every drift case fails-closed via
// t.Fatalf (no silent skip, no swallowed error). The default-ACTIVE
// invariant is the load-bearing assertion — a future refactor that
// silently drops it would surface as test failure in case (1).
//
// Pre-existing build carry-forward (per PRE-EXISTING-BUILD-ISSUES-2026-07-04):
// tests/e2e package compilation is currently blocked by
// qdrant_e2e_youtube_test.go:625 (undefined: youtubetypes.ClipMetadata).
// This file compiles in isolation (gofmt clean + syntactically valid);
// the carry-forward is documented per AGENTS.md pre-existing build
// issues convention.
package e2e

import (
	"strings"
	"testing"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
)

// TestQdrantDoDLifecycleFilter_CompileFilterContainsActiveClause is the
// canonical hermetic TDD lock for Gate 9. The compiled filter is the
// canonical wire body sent to Qdrant; the lifecycle_state condition in
// min_should is the load-bearing invariant for excluding non-ACTIVE points.
func TestQdrantDoDLifecycleFilter_CompileFilterContainsActiveClause(t *testing.T) {
	t.Run("default_empty_lifecycle_state_emits_active_condition", func(t *testing.T) {
		scope := appsearch.SearchScope{IsSystem: false, WorkspaceID: "ws_test_001"}
		filter := appsearch.AssetFilter{} // empty LifecycleState → defaults to ["ACTIVE"]

		compiled, err := qdrantsearch.CompileQdrantFilter(scope, filter)
		if err != nil {
			t.Fatalf("CompileQdrantFilter: unexpected error (fail-closed contract should NOT fire for valid scope): %v", err)
		}

		if !hasLifecycleStateCondition(compiled, "ACTIVE") {
			t.Fatalf("compiled filter missing canonical lifecycle_state=ACTIVE condition (the godlike/06 SSOT default); compiled=%v", compiled)
		}
		if got := minShould(compiled); got != 1 {
			t.Fatalf("min_should.min_count=%d, want 1 (at least one lifecycle state must match); compiled=%v", got, compiled)
		}
	})

	t.Run("caller_supplied_lifecycle_state_emits_all_conditions", func(t *testing.T) {
		scope := appsearch.SearchScope{IsSystem: false, WorkspaceID: "ws_test_002"}
		filter := appsearch.AssetFilter{
			LifecycleState: []string{"ACTIVE", "PUBLISHED"},
		}

		compiled, err := qdrantsearch.CompileQdrantFilter(scope, filter)
		if err != nil {
			t.Fatalf("CompileQdrantFilter: %v", err)
		}

		if !hasLifecycleStateCondition(compiled, "ACTIVE") {
			t.Errorf("compiled filter missing lifecycle_state=ACTIVE condition; compiled=%v", compiled)
		}
		if !hasLifecycleStateCondition(compiled, "PUBLISHED") {
			t.Errorf("compiled filter missing lifecycle_state=PUBLISHED condition; compiled=%v", compiled)
		}
		if got := minShould(compiled); got != 1 {
			t.Errorf("min_should.min_count=%d, want 1; compiled=%v", got, compiled)
		}
	})

	t.Run("admin_override_lifecycle_state_emits_deleted_condition", func(t *testing.T) {
		// godlike/06 SSOT: the IsSystem=true path bypasses the
		// workspace must-clause (admin/reconcile/snapshot use case).
		// The admin can pass LifecycleState=["DELETED"] to
		// surface tombstones for reconciliation — the canonical
		// non-ACTIVE search surface per architecture/current.yaml
		// #id-28.
		scope := appsearch.SearchScope{IsSystem: true, WorkspaceID: ""}
		filter := appsearch.AssetFilter{
			LifecycleState: []string{"DELETED"},
		}

		compiled, err := qdrantsearch.CompileQdrantFilter(scope, filter)
		if err != nil {
			t.Fatalf("CompileQdrantFilter: %v", err)
		}

		if !hasLifecycleStateCondition(compiled, "DELETED") {
			t.Errorf("compiled filter missing lifecycle_state=DELETED condition (admin override); compiled=%v", compiled)
		}
		// The default-ACTIVE lifecycle condition MUST NOT be present when
		// the caller supplies an explicit non-empty allow-list.
		if hasLifecycleStateCondition(compiled, "ACTIVE") {
			t.Errorf("compiled filter contains stale lifecycle_state=ACTIVE condition (default should NOT override caller-supplied allow-list); compiled=%v", compiled)
		}
		if got := minShould(compiled); got != 1 {
			t.Errorf("min_should.min_count=%d, want 1; compiled=%v", got, compiled)
		}
	})

	t.Run("empty_workspace_id_non_system_returns_error", func(t *testing.T) {
		// Per filter_compiler.go §Invariants 1: WorkspaceID must-clause
		// is ALWAYS present when scope.IsSystem is false. An empty
		// WorkspaceID with IsSystem=false returns an error — the
		// fail-closed contract that mirrors mediasearch.Service::Search
		// rejection of ErrMissingWorkspace.
		scope := appsearch.SearchScope{IsSystem: false, WorkspaceID: ""}
		filter := appsearch.AssetFilter{}

		_, err := qdrantsearch.CompileQdrantFilter(scope, filter)
		if err == nil {
			t.Fatal("CompileQdrantFilter: expected error for empty WorkspaceID + IsSystem=false (fail-closed contract), got nil")
		}
		if !strings.Contains(err.Error(), "WorkspaceID is required") {
			t.Errorf("error message should mention WorkspaceID requirement; got: %v", err)
		}
	})

	t.Run("default_workspace_id_non_system_returns_error", func(t *testing.T) {
		// Per filter_compiler.go §Invariants 1: "default" is a
		// reserved sentinel; a real workspace or IsSystem=true is
		// required. The error message names the sentinel verbatim
		// so operator log scans can grep for the drift.
		scope := appsearch.SearchScope{IsSystem: false, WorkspaceID: "default"}
		filter := appsearch.AssetFilter{}

		_, err := qdrantsearch.CompileQdrantFilter(scope, filter)
		if err == nil {
			t.Fatal("CompileQdrantFilter: expected error for reserved WorkspaceID='default' + IsSystem=false, got nil")
		}
		if !strings.Contains(err.Error(), "reserved") {
			t.Errorf("error message should mention 'reserved' sentinel; got: %v", err)
		}
	})

	t.Run("is_system_true_with_empty_workspace_succeeds", func(t *testing.T) {
		// Per filter_compiler.go §Invariants 1: the IsSystem=true
		// admin path bypasses the workspace must-clause. This is
		// the canonical entry point for admin/reconcile/snapshot
		// scans that intentionally cross workspace boundaries.
		scope := appsearch.SearchScope{IsSystem: true, WorkspaceID: ""}
		filter := appsearch.AssetFilter{}

		compiled, err := qdrantsearch.CompileQdrantFilter(scope, filter)
		if err != nil {
			t.Fatalf("CompileQdrantFilter: %v", err)
		}

		// The default-ACTIVE lifecycle condition MUST still be present
		// even on the admin path (lifecycle filter is
		// always-on per filter_compiler.go §Invariants 2).
		if !hasLifecycleStateCondition(compiled, "ACTIVE") {
			t.Errorf("compiled filter missing canonical lifecycle_state=ACTIVE condition on admin path; compiled=%v", compiled)
		}
		if got := minShould(compiled); got != 1 {
			t.Errorf("min_should.min_count=%d, want 1; compiled=%v", got, compiled)
		}
		// The workspace must-clause MUST NOT be present on
		// the admin path (bypass per filter_compiler.go §Invariants 1).
		if hasKeyMustClause(compiled, "workspace_id") {
			t.Errorf("admin path should bypass workspace must-clause; compiled=%v", compiled)
		}
	})
}

// hasLifecycleStateCondition reports whether the compiled filter
// contains a lifecycle condition with key="lifecycle_state" and
// match.value=<expected>. The canonical compiler (filter_compiler.go
// §3) deliberately places the lifecycle allow-list in the structured
// `min_should.conditions` branch with `min_count=1` — states are
// alternatives (ACTIVE+PUBLISHED must not AND together), so the DoD
// gate asserts that branch, not `must`. The match.value is a string per
// filter_compiler.go::matchClause ({"key", "match": {"value", ...}}).
func hasLifecycleStateCondition(compiled map[string]interface{}, expected string) bool {
	return hasKeyClauseWithValue(compiled, "should", "lifecycle_state", expected)
}

// hasKeyClauseWithValue is the generic version — checks for any
// key/match.value pair in the named section ("must" or "should").
// For "should", it unwraps the canonical min_should.conditions branch.
// Used to assert both lifecycle conditions and the workspace_id clause,
// which share the matchClause wire shape.
func hasKeyClauseWithValue(compiled map[string]interface{}, section, key, value string) bool {
	clauses, ok := compiled[section].([]map[string]interface{})
	if !ok && section == "should" {
		structured, structuredOK := compiled["min_should"].(map[string]interface{})
		if structuredOK {
			clauses, ok = structured["conditions"].([]map[string]interface{})
		}
	}
	if !ok {
		return false
	}
	for _, clause := range clauses {
		if k, _ := clause["key"].(string); k != key {
			continue
		}
		match, _ := clause["match"].(map[string]interface{})
		if match == nil {
			continue
		}
		if v, _ := match["value"].(string); v == value {
			return true
		}
	}
	return false
}

// minShould returns the canonical min_should.min_count value from the
// compiled filter (1 per filter_compiler.go §3 — at least one lifecycle
// state must match).
func minShould(compiled map[string]interface{}) int {
	structured, ok := compiled["min_should"].(map[string]interface{})
	if !ok {
		return -1
	}
	switch v := structured["min_count"].(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return -1
	}
}

// hasKeyMustClause is a convenience wrapper — checks for any
// must-clause with the given key (regardless of value). Used to
// assert the workspace_id clause is absent on the admin path.
func hasKeyMustClause(compiled map[string]interface{}, key string) bool {
	must, ok := compiled["must"].([]map[string]interface{})
	if !ok {
		return false
	}
	for _, clause := range must {
		if k, _ := clause["key"].(string); k == key {
			return true
		}
	}
	return false
}
