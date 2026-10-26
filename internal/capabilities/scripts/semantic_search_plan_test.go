package scriptgeneration

import (
	"strings"
	"testing"
)

func feedbackPlan(t *testing.T, queries []SemanticSearchQuery, entities ...string) *SemanticProductionPlan {
	t.Helper()
	consolidation := planConsolidation([]SemanticProductionCandidate{
		{
			ID: "vc_0001", Recipe: "entity_search", Rank: 1, SceneID: "s01",
			EntityIDs: entities, AssetKey: "entity:" + entities[0], RequiresMedia: true,
			Queries: queries, Title: "entity", Evidence: "Alberto Stasi",
		},
	}, nil)
	plan, err := BuildSemanticProductionPlan(consolidation, planIdentities(), DefaultSemanticProductionPlanConfig())
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	return plan
}

func TestSemanticSearchFeedbackBroadensThenSkipsTheVisual(t *testing.T) {
	plan := feedbackPlan(t, []SemanticSearchQuery{
		{Kind: "exact_mention", Text: "Alberto Stasi"},
		{Kind: "context_phrase", Text: "Alberto Stasi torna"},
		{Kind: "source_sentence", Text: "Alberto Stasi torna nel racconto."},
	}, "person_01")
	attempt, ok := plan.NextSearchAttempt()
	if !ok || attempt.QueryKind != "exact_mention" {
		t.Fatalf("first attempt: %+v ok=%v", attempt, ok)
	}
	for rung, wantKind := range []string{"context_phrase", "source_sentence"} {
		next, more, err := plan.RecordSearchOutcome(SemanticSearchOutcome{CandidateID: "vc_0001", Rung: rung})
		if err != nil {
			t.Fatalf("rung %d: %v", rung, err)
		}
		if !more || next.Rung != rung+1 || next.QueryKind != wantKind {
			t.Fatalf("rung %d must widen to %s: %+v ok=%v", rung, wantKind, next, more)
		}
		if plan.Units[0].Retry == nil || plan.Units[0].Retry.Action != "broaden" {
			t.Fatalf("a failed rung reports broaden: %+v", plan.Units[0].Retry)
		}
	}
	next, more, err := plan.RecordSearchOutcome(SemanticSearchOutcome{CandidateID: "vc_0001", Rung: 2, Error: "provider returned no image"})
	if err != nil {
		t.Fatalf("exhausted ladder: %v", err)
	}
	if more || next.CandidateID != "" {
		t.Fatalf("an exhausted single-name ladder settles the plan: %+v ok=%v", next, more)
	}
	unit := plan.Units[0]
	if unit.SearchStatus != ProductionSearchFailed || unit.Status != ProductionUnitSkipped {
		t.Fatalf("unit must be skipped, not retried forever: %+v", unit)
	}
	if unit.Retry == nil || unit.Retry.Action != "skip_visual" || !strings.Contains(unit.Retry.Reason, "provider returned no image") {
		t.Fatalf("skip_visual must carry the provider reason: %+v", unit.Retry)
	}
	if plan.SearchableUnits() != 0 || plan.Metrics.SearchesSkipped != 1 || plan.Metrics.SearchesFailed != 1 || plan.Metrics.SearchBroadens != 2 {
		t.Fatalf("search metrics: %+v searchable=%d", plan.Metrics, plan.SearchableUnits())
	}
	feedback := plan.SearchFeedback()
	if feedback == nil || len(feedback.Entries) != 1 {
		t.Fatalf("feedback projection: %+v", feedback)
	}
	entry := feedback.Entries[0]
	if entry.Status != ProductionSearchFailed || entry.Retry == nil || entry.Retry.Action != "skip_visual" {
		t.Fatalf("feedback must report the failure in the engine contract: %+v", entry)
	}
	if entry.ProposalID != "vc_0001" || entry.SceneID != "s01" || len(entry.Queries) != 3 {
		t.Fatalf("feedback entry identity: %+v", entry)
	}
}

func TestContextQueryKeepsItsIllustrativeProvenanceThroughPlanning(t *testing.T) {
	query := SemanticSearchQuery{
		Kind: "context_phrase", Text: "Alberto Stasi processo giudiziario",
		Origin: "derived_from_source_context", Illustrative: true,
		Evidence: "Alberto Stasi affronta un processo giudiziario lungo.",
	}
	plan := feedbackPlan(t, []SemanticSearchQuery{
		{Kind: "exact_mention", Text: "Alberto Stasi", Origin: "literal"}, query,
	}, "person_01")
	first, ok := plan.NextSearchAttempt()
	if !ok {
		t.Fatal("missing exact query")
	}
	second, more, err := plan.RecordSearchOutcome(SemanticSearchOutcome{
		CandidateID: first.CandidateID, Rung: first.Rung,
	})
	if err != nil || !more {
		t.Fatalf("context query did not follow exact query: %+v %v %v", second, more, err)
	}
	if second.QueryOrigin != query.Origin || !second.Illustrative {
		t.Fatalf("query provenance was lost: %+v", second)
	}
	feedback := plan.SearchFeedback()
	if len(feedback.Entries) != 1 || len(feedback.Entries[0].Queries) != 2 ||
		feedback.Entries[0].Queries[1].Origin != query.Origin || !feedback.Entries[0].Queries[1].Illustrative {
		t.Fatalf("feedback lost query provenance: %+v", feedback)
	}
}

func TestSemanticSearchFeedbackSettlesASatisfiedAsset(t *testing.T) {
	plan := feedbackPlan(t, []SemanticSearchQuery{{Kind: "exact_mention", Text: "Alberto Stasi"}}, "person_01")
	if _, more, err := plan.RecordSearchOutcome(SemanticSearchOutcome{CandidateID: "vc_0001", Rung: 0, Found: true, AssetID: "asset_42"}); err != nil || more {
		t.Fatalf("a hit settles the only search: more=%v err=%v", more, err)
	}
	unit := plan.Units[0]
	if unit.SearchStatus != ProductionSearchSatisfied || unit.ResolvedAssetID != "asset_42" || unit.Retry != nil {
		t.Fatalf("satisfied unit: %+v", unit)
	}
	if plan.Metrics.SearchesSatisfied != 1 || plan.Metrics.SearchesFailed != 0 {
		t.Fatalf("satisfaction metrics: %+v", plan.Metrics)
	}
	if _, more, err := plan.RecordSearchOutcome(SemanticSearchOutcome{CandidateID: "vc_0001", Rung: 0}); err == nil {
		t.Fatalf("a settled composition cannot be searched again: more=%v", more)
	}
}

func TestSemanticSearchFeedbackDropsAnEntityOnlyWhenANameWasNotSearched(t *testing.T) {
	// The main ladder searched Alberto Stasi and the source sentence; Andrea
	// Sempio was mentioned but the ladder never carried his name, so a failure
	// narrows the composition to him instead of dropping the visual.
	plan := feedbackPlan(t, []SemanticSearchQuery{
		{Kind: "exact_mention", Text: "Alberto Stasi"},
		{Kind: "source_sentence", Text: "Alberto Stasi e Andrea Sempio nel procedimento."},
	}, "person_01", "person_02")
	if len(plan.Units[0].EntityLadders) != 1 || plan.Units[0].EntityLadders[0].EntityID != "person_02" {
		t.Fatalf("the untried name must become the drop fallback: %+v", plan.Units[0].EntityLadders)
	}
	if _, _, err := plan.RecordSearchOutcome(SemanticSearchOutcome{CandidateID: "vc_0001", Rung: 0}); err != nil {
		t.Fatal(err)
	}
	next, more, err := plan.RecordSearchOutcome(SemanticSearchOutcome{CandidateID: "vc_0001", Rung: 1})
	if err != nil || !more {
		t.Fatalf("exhausted ladder must drop the searched name: more=%v err=%v", more, err)
	}
	if next.Query != "Andrea Sempio" || plan.Units[0].Retry == nil || plan.Units[0].Retry.Action != "drop_entity" {
		t.Fatalf("drop_entity retries the remaining identity: %+v retry=%+v", next, plan.Units[0].Retry)
	}
	if len(plan.Units[0].EntityIDs) != 1 || plan.Units[0].EntityIDs[0] != "person_02" || plan.Metrics.EntityDrops != 1 {
		t.Fatalf("the composition keeps only the remaining identity: %+v", plan.Units[0])
	}
	if _, more, err := plan.RecordSearchOutcome(SemanticSearchOutcome{CandidateID: "vc_0001", Rung: 0}); err != nil || more {
		t.Fatalf("the last name settles the plan: more=%v err=%v", more, err)
	}
	if plan.Units[0].Retry == nil || plan.Units[0].Retry.Action != "skip_visual" {
		t.Fatalf("an exhausted last name skips the visual: %+v", plan.Units[0].Retry)
	}
}

func TestSemanticSearchFeedbackNeverSpendsAReusedAssetTwice(t *testing.T) {
	stasi := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
		planCandidate("vc_0002", "entity_search", "s02", 2, []string{"person_01"}, "entity:person_01", stasi),
	}, nil)
	plan, err := BuildSemanticProductionPlan(consolidation, planIdentities(), DefaultSemanticProductionPlanConfig())
	if err != nil {
		t.Fatal(err)
	}
	if plan.SearchableUnits() != 1 {
		t.Fatalf("a reused asset is not a second search: %d", plan.SearchableUnits())
	}
	if _, _, err := plan.RecordSearchOutcome(SemanticSearchOutcome{CandidateID: "vc_0002", Rung: 0}); err == nil {
		t.Fatal("a reused composition has no ladder to execute")
	}
	feedback := plan.SearchFeedback()
	if len(feedback.Entries) != 1 || feedback.Entries[0].ProposalID != "vc_0001" {
		t.Fatalf("only searched compositions report feedback: %+v", feedback.Entries)
	}
}

func TestSemanticSearchFeedbackRevokesCoverageAndImageAdmissionOnSkip(t *testing.T) {
	consolidation := planConsolidation([]SemanticProductionCandidate{
		{
			ID: "vc_0001", Recipe: "entity_search", Rank: 1, SceneID: "s01",
			EntityIDs: []string{"person_01"}, AssetKey: "entity:person_01", RequiresMedia: true,
			Queries: []SemanticSearchQuery{{Kind: "exact_mention", Text: "Alberto Stasi"}},
			FactIDs: []string{"cf_0001"}, CoveredFactIDs: []string{"cf_0001"},
		},
	}, []SemanticConsolidatedFact{{
		ID: "cf_0001", Representation: &SemanticFactRepresentation{CandidateID: "vc_0001", Recipe: "entity_search"},
	}})
	plan, err := BuildSemanticProductionPlan(consolidation, planIdentities(), DefaultSemanticProductionPlanConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.AdmitsIdentity("Alberto Stasi") || plan.Coverage.CoveredFacts != 1 {
		t.Fatalf("baseline admission/coverage: %+v %+v", plan.Identity, plan.Coverage)
	}
	if _, more, err := plan.RecordSearchOutcome(SemanticSearchOutcome{CandidateID: "vc_0001", Rung: 0, Error: "no image"}); err != nil || more {
		t.Fatalf("skip: more=%v err=%v", more, err)
	}
	if plan.Coverage.CoveredFacts != 0 || plan.Coverage.DeferredFacts != 1 || len(plan.Coverage.Deferred) != 1 {
		t.Fatalf("a skipped visual defers its facts: %+v", plan.Coverage)
	}
	if !strings.Contains(plan.Coverage.Deferred[0].Reason, "no image") {
		t.Fatalf("the deferral carries the provider reason: %+v", plan.Coverage.Deferred[0])
	}
	if plan.AdmitsIdentity("Alberto Stasi") {
		t.Fatal("an identity whose only search failed must lose its image admission")
	}
	if plan.Metrics.CoveredFacts != 0 || plan.Metrics.DeferredFacts != 1 {
		t.Fatalf("coverage metrics after skip: %+v", plan.Metrics)
	}
}
