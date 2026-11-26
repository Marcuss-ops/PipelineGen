package scriptgeneration

import (
	"strings"
	"testing"

	generationcontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/generation"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func planIdentities() []SemanticIdentity {
	return []SemanticIdentity{
		{ID: "person_01", CanonicalSurface: "Alberto Stasi", Kind: "PERSON"},
		{ID: "person_02", CanonicalSurface: "Andrea Sempio", Kind: "PERSON"},
	}
}

func planCandidate(id, recipe, sceneID string, rank int, entities []string, assetKey string, queries ...generationcontract.SemanticSearchQuery) SemanticProductionCandidate {
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
	stasi := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	sempio := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Andrea Sempio"}
	// The engine emits candidates already ranked and ordered; Go preserves that
	// order and only applies the media budget.
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
		planCandidate("vc_0002", "timeline", "s02", 2, nil, "timeline:g1"),
		planCandidate("vc_0003", "entity_search", "s03", 3, []string{"person_02"}, "entity:person_02", sempio),
	}, nil)
	plan, err := BuildSemanticProductionPlan(consolidation, planIdentities(), SemanticProductionPlanConfig{MaxMediaUnits: 1, MaxMediaUnitsPerScene: 2})
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	if plan.Schema != SemanticProductionPlanSchema {
		t.Fatalf("schema: %q", plan.Schema)
	}
	byID := map[string]generationcontract.SemanticProductionUnit{}
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

func TestGoPreservesTheEngineEditorialOrder(t *testing.T) {
	contextual := generationcontract.SemanticSearchQuery{
		Kind: "context_phrase", Text: "Andrea Sempio treatment plant Toledo",
		Origin: "derived_from_source_context", Evidence: "Andrea Sempio described a treatment plant in Toledo.",
		Components: []generationcontract.SemanticSearchQueryComponent{
			{Kind: "entity", ComponentType: "PERSON", Text: "Andrea Sempio"},
			{Kind: "object", ComponentType: "OBJECT", Text: "treatment plant"},
			{Kind: "location", ComponentType: "LOCATION", Text: "Toledo"},
		},
	}
	bare := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Ann Arbor", Origin: "literal"}
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
	// BrainRust owns editorial ranking. Even though the second candidate is a
	// well-grounded composition, Go must not promote it over the engine order.
	if got := plan.Units[0].CandidateID; got != "vc_bare" {
		t.Fatalf("Go must keep the engine order, got first unit %s", got)
	}
	if got := plan.Units[0].Priority; got != 0.82 {
		t.Fatalf("Go must carry the engine priority verbatim, got %v", got)
	}
	if plan.Units[1].Status != ProductionUnitDroppedBudget {
		t.Fatalf("the second engine candidate waits for the next slot: %+v", plan.Units[1])
	}
}

func TestSemanticPlanSkipsBarePlaceAndAbstractVisualQueries(t *testing.T) {
	identities := []SemanticIdentity{
		{ID: "location_01", CanonicalSurface: "Stati Uniti", Kind: "LOCATION"},
		{ID: "concept_01", CanonicalSurface: "biografia sportiva", Kind: "VISUAL_CONCEPT"},
		{ID: "location_02", CanonicalSurface: "Grand Rapids", Kind: "LOCATION"},
	}
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_place", "entity_search", "s01", 1, []string{"location_01"}, "entity:location_01",
			generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Stati Uniti", Origin: "literal"}),
		planCandidate("vc_concept", "entity_search", "s01", 2, []string{"concept_01"}, "entity:concept_01",
			generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "biografia sportiva", Origin: "literal"}),
		planCandidate("vc_context", "entity_search", "s02", 3, []string{"location_02"}, "entity:location_02",
			generationcontract.SemanticSearchQuery{Kind: "context_phrase", Text: "Floyd Mayweather Grand Rapids", Origin: "derived_from_source_context", Evidence: "Floyd Mayweather was born in Grand Rapids.", Components: []generationcontract.SemanticSearchQueryComponent{{Text: "Floyd Mayweather"}, {Text: "Grand Rapids"}}}),
	}, nil)
	plan, err := BuildSemanticProductionPlan(consolidation, identities, DefaultSemanticProductionPlanConfig())
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	byID := map[string]generationcontract.SemanticProductionUnit{}
	for _, unit := range plan.Units {
		byID[unit.CandidateID] = unit
	}
	for _, id := range []string{"vc_place", "vc_concept"} {
		if byID[id].Status != ProductionUnitSkipped || !strings.Contains(byID[id].DroppedReason, "weak_visual_query") {
			t.Fatalf("bare location/concept query should be skipped with a reason: %+v", byID[id])
		}
	}
	if byID["vc_context"].Status != ProductionUnitPlanned || byID["vc_context"].SearchStatus != ProductionSearchPending {
		t.Fatalf("grounded source context should remain searchable: %+v", byID["vc_context"])
	}
}

func TestDocumentAnchorKeepsItsCrossScenePriority(t *testing.T) {
	anchor := planCandidate("vc_anchor", "entity_search", "s01", 1, nil, "anchor:toledo",
		generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Toledo", Origin: "literal"})
	anchor.Priority = 0.93
	anchor.Rationale = []string{"global_document_anchor"}
	plan, err := BuildSemanticProductionPlan(planConsolidation([]SemanticProductionCandidate{anchor}, nil), nil, DefaultSemanticProductionPlanConfig())
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	if len(plan.Units) != 1 || plan.Units[0].Priority != 0.93 {
		t.Fatalf("document anchor priority was changed: %+v", plan.Units)
	}
}

func TestSemanticProductionPlanReusesOneRetrievalPerAsset(t *testing.T) {
	stasi := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
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
	stasi := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	sempio := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Andrea Sempio"}
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
	stasi := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
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
	stasi := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
	}, nil)
	result := &ScriptSemanticResult{
		Identities:    planIdentities(),
		Consolidation: consolidation,
		Scenes:        map[string]*SemanticCandidateBundle{"s01": {}, "s02": {}},
	}
	view, plan, feedback, err := PlanScriptProduction(result)
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
	if view, plan, feedback, err := PlanScriptProduction(&ScriptSemanticResult{Scenes: map[string]*SemanticCandidateBundle{"s01": {}}}); err != nil || view != nil || plan != nil || feedback != nil {
		t.Fatalf("a worker without the projection must keep planning unchanged: %v %v %v %v", view, plan, feedback, err)
	}
}

func TestCallerImageSearchesEnterTheSceneLadderAndRespectLockedAssignments(t *testing.T) {
	query := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	candidate := planCandidate("vc_0001", "entity_search", "scene_01", 1, []string{"person_01"}, "entity:person_01", query)
	candidate.SceneIDs = []string{"segment_01"}
	plan, err := BuildSemanticProductionPlan(planConsolidation([]SemanticProductionCandidate{candidate}, nil), planIdentities(), DefaultSemanticProductionPlanConfig())
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	searches := []mediadomain.SegmentMediaSearch{
		{SegmentID: "segment_01", Slot: mediadomain.SlotSecondaryImage, Query: "Stasi processo Milano", Providers: []string{"internet_images"}, MediaTypes: []string{"image"}},
	}
	added := ApplyCallerImageSearches(plan, searches, nil)
	if added != 1 || plan.Metrics.PlannedQueries != 2 {
		t.Fatalf("caller search was not included in plan metrics: added=%d metrics=%+v", added, plan.Metrics)
	}
	attempt, ok := plan.NextSearchAttempt()
	if !ok || attempt.QueryOrigin != "caller_media_plan" || attempt.Query != "Stasi processo Milano" {
		t.Fatalf("caller query must pass through unchanged and run first: %+v ok=%v", attempt, ok)
	}
	locked := &SemanticProductionPlan{Units: append([]generationcontract.SemanticProductionUnit(nil), plan.Units...), IdentityByID: plan.IdentityByID}
	locked.Metrics.PlannedQueries = plan.Metrics.PlannedQueries
	added = ApplyCallerImageSearches(locked, searches, []mediadomain.SegmentMediaAssignment{{SegmentID: "segment_01", Slot: mediadomain.SlotSecondaryImage, Locked: true}})
	if added != 0 || locked.Metrics.PlannedQueries != plan.Metrics.PlannedQueries {
		t.Fatalf("locked image assignment must suppress caller search: added=%d metrics=%+v", added, locked.Metrics)
	}
}

func TestAdoptSemanticPlannerViewRecordsThePlan(t *testing.T) {
	stasi := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi),
	}, nil)
	result := &ScriptSemanticResult{
		Identities:    planIdentities(),
		Consolidation: consolidation,
		Scenes:        map[string]*SemanticCandidateBundle{"s01": {}},
	}
	runner := &Runner{}
	out := &phraseImpactEnrichment{}
	req := GenerateRequest{MediaPlan: mediadomain.MediaPlanSpec{Searches: []mediadomain.SegmentMediaSearch{{
		SegmentID: "s01", Slot: mediadomain.SlotSecondaryImage, Query: "Alberto Stasi trial Milan",
	}}}}
	runner.adoptSemanticPlannerView(out, result, req)
	if out.consolidation != consolidation || out.productionPlan == nil || out.searchFeedback == nil {
		t.Fatalf("the enrichment must carry the planner view: %+v", out)
	}
	if len(out.searchFeedback.Entries) != 1 || out.searchFeedback.Entries[0].ProposalID != "vc_0001" {
		t.Fatalf("feedback must be projected onto the enrichment: %+v", out.searchFeedback)
	}
	if got := out.productionPlan.Units[0].Queries[0].Origin; got != "caller_media_plan" {
		t.Fatalf("caller media search was not carried into the runtime plan: %q", got)
	}
	empty := &phraseImpactEnrichment{}
	(&Runner{}).adoptSemanticPlannerView(empty, nil, GenerateRequest{})
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
			generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "X"}),
	}, nil)
	if _, err := BuildSemanticProductionPlan(broken, planIdentities(), DefaultSemanticProductionPlanConfig()); err == nil {
		t.Fatal("an unknown identity must fail closed")
	}
	broken = planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01",
			generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}),
	}, []SemanticConsolidatedFact{{ID: "cf_0001", Representation: &SemanticFactRepresentation{CandidateID: "vc_0001"}}})
	if _, err := BuildSemanticProductionPlan(broken, planIdentities(), DefaultSemanticProductionPlanConfig()); err == nil {
		t.Fatal("a representation that does not declare its fact must fail closed")
	}
}
