package scriptgeneration

import (
	"encoding/json"
	"testing"

	generationcontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/generation"
)

func buildPersonEntityMaxPlan(t *testing.T, candidates []SemanticProductionCandidate) *SemanticProductionPlan {
	t.Helper()
	plan, err := BuildSemanticProductionPlan(
		planConsolidation(candidates, nil),
		planIdentities(),
		DefaultSemanticProductionPlanConfig(),
	)
	if err != nil {
		t.Fatalf("build semantic production plan: %v", err)
	}
	return plan
}

func TestPersonEntityMaxSinglePersonPlansOneQuery(t *testing.T) {
	stasi := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	plan := buildPersonEntityMaxPlan(t, []SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
	})
	if len(plan.Units) != 1 {
		t.Fatalf("units = %d, want one", len(plan.Units))
	}
	unit := plan.Units[0]
	if unit.Status != ProductionUnitPlanned || unit.SearchStatus != ProductionSearchPending || len(unit.Queries) != 1 {
		t.Fatalf("single-person unit = %+v, want one pending planned query", unit)
	}
	if plan.Metrics.MediaUnits != 1 || plan.Metrics.PlannedQueries != 1 {
		t.Fatalf("single-person metrics = %+v, want 1 media unit and 1 query", plan.Metrics)
	}
}

func TestPersonEntityMaxTwoPeoplePlanTwoQueries(t *testing.T) {
	stasi := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	sempio := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Andrea Sempio"}
	plan := buildPersonEntityMaxPlan(t, []SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
		planCandidate("vc_0002", "entity_search", "s01", 2, []string{"person_02"}, "entity:person_02", sempio),
	})
	if len(plan.Units) != 2 {
		t.Fatalf("units = %d, want two people in one scene", len(plan.Units))
	}
	queries := make(map[string]bool, len(plan.Units))
	for _, unit := range plan.Units {
		if unit.Status != ProductionUnitPlanned || unit.SearchStatus != ProductionSearchPending || len(unit.Queries) != 1 {
			t.Fatalf("two-person unit = %+v, want one pending planned query", unit)
		}
		queries[unit.Queries[0].Text] = true
	}
	for _, want := range []string{"Alberto Stasi", "Andrea Sempio"} {
		if !queries[want] {
			t.Fatalf("missing grounded query %q in %+v", want, queries)
		}
	}
	if plan.Metrics.MediaUnits != 2 || plan.Metrics.PlannedQueries != 2 {
		t.Fatalf("two-person metrics = %+v, want 2 media units and 2 queries", plan.Metrics)
	}
}

func TestPersonEntityMaxPlanReplayIsByteIdentical(t *testing.T) {
	stasi := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	sempio := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Andrea Sempio"}
	build := func() []byte {
		t.Helper()
		plan := buildPersonEntityMaxPlan(t, []SemanticProductionCandidate{
			planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
			planCandidate("vc_0002", "entity_search", "s01", 2, []string{"person_02"}, "entity:person_02", sempio),
		})
		raw, err := json.Marshal(plan)
		if err != nil {
			t.Fatalf("marshal semantic production plan: %v", err)
		}
		return raw
	}
	first := build()
	second := build()
	if len(first) == 0 {
		t.Fatal("replayed plan is empty")
	}
	if string(first) != string(second) {
		t.Fatalf("plan replay is not byte-identical:\nfirst:  %s\nsecond: %s", first, second)
	}
}

// TestPersonEntityMaxDistinctAssetsAreNotReused proves that the planner keys
// reuse by asset, not by person. It does not claim the retired Rust planner's
// candidate-level "prefer unused" selection, which has no Go equivalent.
func TestPersonEntityMaxDistinctAssetsAreNotReused(t *testing.T) {
	stasi := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	plan := buildPersonEntityMaxPlan(t, []SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
		planCandidate("vc_0002", "entity_search", "s02", 2, []string{"person_01"}, "entity:person_01:alt", stasi),
	})
	if len(plan.Units) != 2 {
		t.Fatalf("units = %d, want two distinct assets", len(plan.Units))
	}
	for _, unit := range plan.Units {
		if unit.Status != ProductionUnitPlanned || unit.SearchStatus != ProductionSearchPending || len(unit.Queries) != 1 {
			t.Fatalf("distinct-asset unit = %+v, want one pending planned query", unit)
		}
	}
	if plan.Metrics.ReusedAssets != 0 || plan.Metrics.MediaUnits != 2 {
		t.Fatalf("distinct-asset metrics = %+v, want no reuse and two media units", plan.Metrics)
	}
}
