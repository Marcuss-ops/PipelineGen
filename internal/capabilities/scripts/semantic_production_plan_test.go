package scriptgeneration

import (
	"strings"
	"testing"
)

func planIdentities() []SemanticIdentity {
	return []SemanticIdentity{
		{ID: "person_01", CanonicalSurface: "Alberto Stasi", Kind: "PERSON"},
		{ID: "person_02", CanonicalSurface: "Andrea Sempio", Kind: "PERSON"},
	}
}

func planCandidate(id, recipe, sceneID string, rank int, entities []string, assetKey string, queries ...SemanticSearchQuery) SemanticProductionCandidate {
	return SemanticProductionCandidate{
		ID: id, Recipe: recipe, Rank: rank, SceneID: sceneID, EntityIDs: entities,
		AssetKey: assetKey, RequiresMedia: len(queries) > 0, Queries: queries,
		Title: recipe + " " + id,
	}
}

func planConsolidation(candidates []SemanticProductionCandidate, facts []SemanticConsolidatedFact) *SemanticVisualConsolidation {
	return &SemanticVisualConsolidation{
		Schema:               "visual_consolidation.v1",
		ProductionCandidates: candidates,
		Facts:                facts,
	}
}

func TestSemanticProductionPlanAppliesTheRunBudgetDeterministically(t *testing.T) {
	stasi := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	sempio := SemanticSearchQuery{Kind: "exact_mention", Text: "Andrea Sempio"}
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0003", "entity_search", "s03", 3, []string{"person_02"}, "entity:person_02", sempio),
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
		planCandidate("vc_0002", "timeline", "s02", 2, nil, "timeline:g1"),
	}, nil)
	plan, err := BuildSemanticProductionPlan(consolidation, planIdentities(), SemanticProductionPlanConfig{MaxMediaUnits: 1, MaxMediaUnitsPerScene: 2})
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	if plan.Schema != SemanticProductionPlanSchema {
		t.Fatalf("schema: %q", plan.Schema)
	}
	byID := map[string]SemanticProductionUnit{}
	for _, unit := range plan.Units {
		byID[unit.CandidateID] = unit
	}
	if byID["vc_0001"].Status != ProductionUnitPlanned || byID["vc_0001"].SearchStatus != ProductionSearchPending {
		t.Fatalf("highest-rank search must be admitted: %+v", byID["vc_0001"])
	}
	if byID["vc_0003"].Status != ProductionUnitDroppedBudget || !strings.Contains(byID["vc_0003"].DroppedReason, "budget") {
		t.Fatalf("second search must be deferred with a reason: %+v", byID["vc_0003"])
	}
	if byID["vc_0002"].Status != ProductionUnitPlanned || byID["vc_0002"].RequiresMedia {
		t.Fatalf("a text composition is never budgeted as media: %+v", byID["vc_0002"])
	}
	if plan.Metrics.RawCandidates != 3 || plan.Metrics.AdmittedUnits != 2 || plan.Metrics.MediaUnits != 1 || plan.Metrics.PlannedQueries != 1 {
		t.Fatalf("measured plan metrics: %+v", plan.Metrics)
	}
	if got, ok := plan.NextSearchAttempt(); !ok || got.CandidateID != "vc_0001" || got.Query != "Alberto Stasi" {
		t.Fatalf("first attempt must be the top-ranked query: %+v ok=%v", got, ok)
	}
}

func TestGroundedContextQueryWinsScarceSlotOverBareEntity(t *testing.T) {
	contextual := SemanticSearchQuery{
		Kind: "context_phrase", Text: "Andrea Sempio treatment plant Toledo",
		Origin: "derived_from_source_context", Evidence: "Andrea Sempio described a treatment plant in Toledo.",
		Components: []SemanticSearchQueryComponent{
			{Kind: "entity", ComponentType: "PERSON", Text: "Andrea Sempio"},
			{Kind: "object", ComponentType: "OBJECT", Text: "treatment plant"},
			{Kind: "location", ComponentType: "LOCATION", Text: "Toledo"},
		},
	}
	bare := SemanticSearchQuery{Kind: "exact_mention", Text: "Ann Arbor", Origin: "literal"}
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_bare", "entity_search", "s01", 1, []string{"person_01"}, "entity:bare", bare),
		planCandidate("vc_context", "entity_search", "s02", 2, []string{"person_02"}, "context:event", contextual),
	}, nil)
	consolidation.ProductionCandidates[0].Priority = 0.82
	consolidation.ProductionCandidates[1].Priority = 0.68
	plan, err := BuildSemanticProductionPlan(consolidation, planIdentities(), SemanticProductionPlanConfig{
		MaxMediaUnits: 1, MaxMediaUnitsPerScene: 1,
	})
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	if got := plan.Units[0].CandidateID; got != "vc_context" {
		t.Fatalf("grounded context should take the only media slot, got %s", got)
	}
	if plan.Units[1].Status != ProductionUnitDroppedBudget {
		t.Fatalf("bare identity should remain available after the slot is spent: %+v", plan.Units[1])
	}
}

func TestDocumentAnchorKeepsItsCrossScenePriority(t *testing.T) {
	anchor := planCandidate("vc_anchor", "entity_search", "s01", 1, nil, "anchor:toledo",
		SemanticSearchQuery{Kind: "exact_mention", Text: "Toledo", Origin: "literal"})
	anchor.Priority = 0.93
	anchor.Rationale = []string{"global_document_anchor"}
	if got := semanticPlannerPriority(anchor, nil); got != 0.93 {
		t.Fatalf("document anchor priority was changed: %v", got)
	}
}

func TestSemanticProductionPlanReusesOneRetrievalPerAsset(t *testing.T) {
	stasi := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
		planCandidate("vc_0002", "entity_search", "s01", 2, []string{"person_01"}, "entity:person_01", stasi),
	}, nil)
	plan, err := BuildSemanticProductionPlan(consolidation, planIdentities(), DefaultSemanticProductionPlanConfig())
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	second := plan.Units[1]
	if second.Status != ProductionUnitReuse || second.ReusedFrom != "vc_0001" || len(second.Queries) != 0 {
		t.Fatalf("shared asset must reuse the first retrieval: %+v", second)
	}
	if plan.Metrics.ReusedAssets != 1 || plan.Metrics.MediaUnits != 1 || plan.Metrics.DuplicateQueriesDrop != 1 {
		t.Fatalf("asset reuse metrics: %+v", plan.Metrics)
	}
	if plan.SearchableUnits() != 1 {
		t.Fatalf("only the owning composition searches: %d", plan.SearchableUnits())
	}
}

func TestSemanticProductionPlanDefersFactsWhoseRepresentationDidNotFit(t *testing.T) {
	stasi := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	sempio := SemanticSearchQuery{Kind: "exact_mention", Text: "Andrea Sempio"}
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
		planCandidate("vc_0002", "entity_search", "s02", 2, []string{"person_02"}, "entity:person_02", sempio),
	}, []SemanticConsolidatedFact{
		{ID: "cf_0001", Representation: &SemanticFactRepresentation{CandidateID: "vc_0001", Recipe: "entity_search"}},
		{ID: "cf_0002", Representation: &SemanticFactRepresentation{CandidateID: "vc_0002", Recipe: "entity_search"}},
		{ID: "cf_0003", UncoveredReason: "the source states no visual shape for this claim"},
	})
	// The plan declares the facts its candidates represent.
	consolidation.ProductionCandidates[0].FactIDs = []string{"cf_0001"}
	consolidation.ProductionCandidates[0].CoveredFactIDs = []string{"cf_0001"}
	consolidation.ProductionCandidates[1].FactIDs = []string{"cf_0002"}
	consolidation.ProductionCandidates[1].CoveredFactIDs = []string{"cf_0002"}
	plan, err := BuildSemanticProductionPlan(consolidation, planIdentities(), SemanticProductionPlanConfig{MaxMediaUnits: 1, MaxMediaUnitsPerScene: 2})
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	if plan.Coverage.CanonicalFacts != 3 || plan.Coverage.CoveredFacts != 1 || plan.Coverage.DeferredFacts != 1 || plan.Coverage.UncoveredFacts != 1 {
		t.Fatalf("coverage accounting: %+v", plan.Coverage)
	}
	if len(plan.Coverage.Deferred) != 1 || !strings.Contains(plan.Coverage.Deferred[0].Reason, "dropped by the run budget") {
		t.Fatalf("deferred fact needs the budget reason: %+v", plan.Coverage.Deferred)
	}
	if plan.Metrics.CoveredFacts != 1 || plan.Metrics.DeferredFacts != 1 || plan.Metrics.UncoveredFacts != 1 {
		t.Fatalf("coverage metrics: %+v", plan.Metrics)
	}
}

func TestSemanticProductionPlanAdmitsOnlyMediaIdentities(t *testing.T) {
	stasi := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
		planCandidate("vc_0002", "fact_card", "s01", 2, []string{"person_02"}, "factcard:cf_0001"),
	}, nil)
	plan, err := BuildSemanticProductionPlan(consolidation, planIdentities(), DefaultSemanticProductionPlanConfig())
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	if !plan.AdmitsIdentity("Alberto Stasi") {
		t.Fatal("a searched identity is admitted for an image")
	}
	if plan.AdmitsIdentity("andrea   sempio") {
		t.Fatal("an identity with only a text card must not be admitted for an image")
	}
	if plan.AdmitsIdentity("Chiara Poggi") {
		t.Fatal("an unknown surface must never be admitted")
	}
	if len(plan.Identity) != 2 || !plan.Identity[0].Media || plan.Identity[1].Media {
		t.Fatalf("planned identity projection: %+v", plan.Identity)
	}
}

func TestPlanScriptProductionCarriesTheConsolidatedViewAndFeedback(t *testing.T) {
	stasi := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
	}, nil)
	bundles := map[string]*SemanticCandidateBundle{
		"s01": {Identities: planIdentities(), Consolidation: consolidation},
		"s02": {Identities: planIdentities(), Consolidation: consolidation},
	}
	view, plan, feedback, err := PlanScriptProduction(bundles)
	if err != nil {
		t.Fatalf("script planning failed: %v", err)
	}
	if view != consolidation || plan == nil || feedback == nil {
		t.Fatalf("the batch must yield the shared view, plan and feedback: %v %v %v", view, plan, feedback)
	}
	if plan.Metrics.MediaUnits != 1 || len(plan.Identity) != 1 {
		t.Fatalf("script plan volumes: %+v identities=%d", plan.Metrics, len(plan.Identity))
	}
	if len(feedback.Entries) != 1 || feedback.Entries[0].ProposalID != "vc_0001" || feedback.Entries[0].Status != "pending" {
		t.Fatalf("planner feedback must stay in the engine contract: %+v", feedback.Entries)
	}
	if view, plan, feedback, err := PlanScriptProduction(map[string]*SemanticCandidateBundle{"s01": {}}); err != nil || view != nil || plan != nil || feedback != nil {
		t.Fatalf("a worker without the projection must keep planning unchanged: %v %v %v %v", view, plan, feedback, err)
	}
}

func TestAdoptSemanticPlannerViewRecordsThePlan(t *testing.T) {
	stasi := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
	}, nil)
	bundles := map[string]*SemanticCandidateBundle{"s01": {Identities: planIdentities(), Consolidation: consolidation}}
	runner := &Runner{}
	out := &phraseImpactEnrichment{}
	runner.adoptSemanticPlannerView(out, bundles)
	if out.consolidation != consolidation || out.productionPlan == nil || out.searchFeedback == nil {
		t.Fatalf("the enrichment must carry the planner view: %+v", out)
	}
	if len(out.searchFeedback.Entries) != 1 || out.searchFeedback.Entries[0].ProposalID != "vc_0001" {
		t.Fatalf("feedback must be projected onto the enrichment: %+v", out.searchFeedback)
	}
	empty := &phraseImpactEnrichment{}
	(&Runner{}).adoptSemanticPlannerView(empty, nil)
	if empty.productionPlan != nil || empty.consolidation != nil {
		t.Fatalf("an empty batch must not invent a plan: %+v", empty)
	}
}

func TestSemanticProductionPlanFailsClosedOnBrokenInput(t *testing.T) {
	if _, err := BuildSemanticProductionPlan(nil, nil, DefaultSemanticProductionPlanConfig()); err == nil {
		t.Fatal("a missing consolidation must fail closed")
	}
	broken := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_99"}, "entity:person_99",
			SemanticSearchQuery{Kind: "exact_mention", Text: "X"}),
	}, nil)
	if _, err := BuildSemanticProductionPlan(broken, planIdentities(), DefaultSemanticProductionPlanConfig()); err == nil {
		t.Fatal("an unknown identity must fail closed")
	}
	broken = planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01",
			SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}),
	}, []SemanticConsolidatedFact{{ID: "cf_0001", Representation: &SemanticFactRepresentation{CandidateID: "vc_0001"}}})
	if _, err := BuildSemanticProductionPlan(broken, planIdentities(), DefaultSemanticProductionPlanConfig()); err == nil {
		t.Fatal("a representation that does not declare its fact must fail closed")
	}
}
