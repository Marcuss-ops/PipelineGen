package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"go.uber.org/zap"
)

// This regression compares every emitted JSON field recursively, not just
// fields known to Go: adding a Rust field cannot silently drop it on decode.
func TestSemanticEngineRealBinaryWireFields(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine not installed; set VELOX_RUST_SEMANTIC_ENGINE_PATH for analytic contract certification")
	}
	scenes := []scriptgen.SemanticSceneInput{
		{SceneID: "first", Text: "Lo Stadio Aurora fu inaugurato nel 1959 a Torino. Il 42% dei tifosi festeggia, contro il 18% del gruppo."},
		{SceneID: "second", Text: "Lo Stadio Aurora fu rinnovato nel 2008 a Torino."},
	}
	request := semanticEngineRequest{Operation: "analyze_script", Language: "it"}
	for _, scene := range scenes {
		request.Scenes = append(request.Scenes, semanticEngineScene{SceneID: scene.SceneID, Text: scene.Text})
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	body, _, err := executor.Run(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var wire semanticEngineResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if !wire.OK || wire.Manifest == nil {
		t.Fatalf("worker failed: %s", body)
	}
	roundtrip, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var expected, actual any
	if err := json.Unmarshal(body, &expected); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(roundtrip, &actual); err != nil {
		t.Fatal(err)
	}
	var compare func(string, any, any)
	compare = func(path string, want, got any) {
		t.Helper()
		switch value := want.(type) {
		case map[string]any:
			object, ok := got.(map[string]any)
			if !ok {
				t.Fatalf("%s lost object", path)
			}
			for key, item := range value {
				projected, ok := object[key]
				if !ok {
					t.Fatalf("%s.%s lost on Go decode", path, key)
				}
				compare(path+"."+key, item, projected)
			}
		case []any:
			items, ok := got.([]any)
			if !ok || len(items) != len(value) {
				t.Fatalf("%s lost array", path)
			}
			for i := range value {
				compare(fmt.Sprintf("%s[%d]", path, i), value[i], items[i])
			}
		default:
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("%s changed: %v -> %v", path, want, got)
			}
		}
	}
	compare("manifest", expected.(map[string]any)["manifest"], actual.(map[string]any)["manifest"])
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := adapter.SemanticBundles(context.Background(), "it", scenes)
	if err != nil {
		t.Fatal(err)
	}
	bundle := bundles["first"]
	if bundle.Analyzers.Lexicon == "" || bundle.StoryGraph == nil || bundle.VisualCoverage == nil || len(bundle.StoryGraph.Nodes) == 0 || bundle.VisualCoverage.TotalFacts == 0 {
		t.Fatalf("script views lost: %+v", bundle)
	}
	if !reflect.DeepEqual(bundle.StoryGraph, wire.Manifest.StoryGraph) || !reflect.DeepEqual(bundle.VisualCoverage, wire.Manifest.VisualCoverage) {
		t.Fatal("script views changed during projection")
	}
}

func resolveSemanticEngineBinary() string {
	if fromEnv := os.Getenv("VELOX_RUST_SEMANTIC_ENGINE_PATH"); fromEnv != "" {
		if info, err := os.Stat(fromEnv); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return fromEnv
		}
	}
	path := filepath.Join("..", "..", "..", "..", "bin", "semantic-engine")
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
		return path
	}
	return ""
}

// TestSemanticEngineRealBinaryAnalysesWholeScript walks the real
// `semantic-engine` binary through the Go adapter: one NDJSON request for two
// scenes returns grounded bullets, a comparable chart, the date and every
// numeric value, with every span verified against the source.
func TestSemanticEngineRealBinaryAnalysesWholeScript(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed; run make rust-fetch")
	}
	first := "Il 42% dei tecnici ha segnalato il problema, contro il 18% del gruppo di controllo."
	second := "Il 15 marzo 2008 è la data del delitto."
	contextQuery := "I tifosi festeggiano sugli spalti dopo la vittoria della squadra."
	executor := NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := adapter.SemanticBundles(context.Background(), "it", []scriptgen.SemanticSceneInput{
		{SceneID: "s1", Text: first},
		{SceneID: "s2", Text: second},
		{SceneID: "s3", Text: contextQuery},
	})
	if err != nil {
		t.Fatalf("real semantic-engine batch failed: %v", err)
	}
	one := bundles["s1"]
	if one == nil || len(one.Bullets) == 0 {
		t.Fatalf("scene s1 bullets missing: %+v", one)
	}
	if one.Consolidation == nil {
		t.Fatalf("manifest omitted the visual consolidation projection: %+v", one)
	}
	if one.Consolidation.Schema != "visual_consolidation.v1" {
		t.Fatalf("consolidation schema: %q", one.Consolidation.Schema)
	}
	if one.Consolidation.Metrics.ProductionCandidates != len(one.Consolidation.ProductionCandidates) {
		t.Fatalf("consolidation candidate count is inconsistent: %+v", one.Consolidation.Metrics)
	}
	if one.Consolidation.Metrics.EstimatedSearchJobs > one.Consolidation.Metrics.RawSearchEntries {
		t.Fatalf("consolidation increased external search jobs: %+v", one.Consolidation.Metrics)
	}
	for _, candidate := range one.Consolidation.ProductionCandidates {
		if candidate.Evidence == "" || candidate.DedupKey == "" || candidate.AssetKey == "" {
			t.Fatalf("production candidate is incomplete: %+v", candidate)
		}
		if candidate.RequiresMedia && len(candidate.Queries) == 0 {
			t.Fatalf("media candidate has no query: %+v", candidate)
		}
	}
	if len(one.Charts) != 2 {
		t.Fatalf("scene s1 charts=%+v, want 25%%-style comparison of 42%% and 18%%", one.Charts)
	}
	for _, chart := range one.Charts {
		if !strings.Contains(first, chart.Text) {
			t.Fatalf("chart candidate %q is not in the source", chart.Text)
		}
	}
	if len(one.Numeric) < 2 {
		t.Fatalf("scene s1 numeric facts=%+v, want both percentages", one.Numeric)
	}
	two := bundles["s2"]
	if two == nil || len(two.Temporal) != 1 {
		t.Fatalf("scene s2 temporal facts missing: %+v", two)
	}
	if two.Temporal[0].NormalizedDate != "2008-03-15" || two.Temporal[0].Precision != "day" {
		t.Fatalf("temporal fact: %+v", two.Temporal[0])
	}
	three := bundles["s3"]
	if three == nil {
		t.Fatal("scene s3 missing from semantic result")
	}
	contextualSearch := false
	phraseSearch := false
	for _, proposal := range three.Proposals {
		if proposal.Type != "entity_search" {
			continue
		}
		for _, query := range proposal.VisualQuerySuggestions {
			if query == contextQuery {
				contextualSearch = true
			}
			if query == "I tifosi festeggiano sugli spalti dopo" {
				phraseSearch = true
			}
		}
	}
	if !contextualSearch {
		t.Fatalf("real worker omitted the grounded sentence query for image/stock search: %+v", three.Proposals)
	}
	if !phraseSearch {
		t.Fatalf("real worker omitted the grounded multiword concept query for image/stock search: %+v", three.Proposals)
	}
}

// TestSemanticEngineRealBinaryConsolidatesGarlascoGold exercises the
// production Go boundary on the reconstructed 2,013-word Garlasco script. It
// proves the selective view survives transport with every span re-verified:
// 8 canonical facts, no uncovered fact, one timeline, three contextual cards
// and far fewer external search jobs than raw mentions.
func TestSemanticEngineRealBinaryConsolidatesGarlascoGold(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed; run make rust-fetch")
	}
	path := filepath.Join("..", "..", "..", "..", "semantic_acceptance", "fixtures", "garlasco_2013_reconstructed.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("Garlasco fixture not available: %v", err)
	}
	var scenes []scriptgen.SemanticSceneInput
	for index, paragraph := range strings.Split(string(data), "\n\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		scenes = append(scenes, scriptgen.SemanticSceneInput{SceneID: fmt.Sprintf("s%02d", index+1), Text: paragraph})
	}
	adapter, err := NewSemanticEngineAdapter(NewSemanticEngineExecutor(binary, resolveFFmpegBinary(), zap.NewNop()))
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := adapter.SemanticBundles(context.Background(), "it", scenes)
	if err != nil {
		t.Fatalf("Garlasco consolidation transport failed: %v", err)
	}
	first := bundles["s01"]
	if first == nil || first.Consolidation == nil {
		t.Fatalf("Garlasco consolidation missing: %+v", first)
	}
	consolidation := first.Consolidation
	if consolidation.Metrics.CanonicalFacts != 8 || consolidation.Metrics.CoveredFacts != 8 || consolidation.Metrics.UncoveredFacts != 0 {
		t.Fatalf("canonical fact coverage: %+v", consolidation.Metrics)
	}
	if consolidation.Metrics.EstimatedSearchJobs >= consolidation.Metrics.RawSearchEntries {
		t.Fatalf("consolidation did not reduce search jobs: %+v", consolidation.Metrics)
	}
	if consolidation.Metrics.ProductionQueries >= consolidation.Metrics.RawQueries {
		t.Fatalf("consolidation did not reduce queries: %+v", consolidation.Metrics)
	}
	var timelines, cards, eventLinks int
	for _, candidate := range consolidation.ProductionCandidates {
		switch candidate.Recipe {
		case "timeline":
			timelines++
		case "fact_card":
			cards++
		}
	}
	eventLinks = len(consolidation.EventLinks)
	if timelines != 1 || cards != 3 || eventLinks != 4 {
		t.Fatalf("selective composition counts: timelines=%d cards=%d event_links=%d", timelines, cards, eventLinks)
	}
	if len(consolidation.ReferenceNumbers) != 1 || consolidation.ReferenceNumbers[0].Surface != "33" {
		t.Fatalf("reference number 33 missing: %+v", consolidation.ReferenceNumbers)
	}
	for _, fact := range consolidation.Facts {
		if fact.Representation == nil && fact.UncoveredReason == "" {
			t.Fatalf("canonical fact is neither represented nor explained: %+v", fact)
		}
	}
}

// TestSemanticEngineRealBinaryPlansGarlascoProduction closes the planner half
// of the consolidation milestone on the real release worker: the transport
// handshake is validated, the Go planner applies the certified run budget to
// the consolidated candidates, assets are retrieved once, every media unit has
// a live query ladder, and a simulated retrieval failure widens and settles in
// the same feedback contract.
func TestSemanticEngineRealBinaryPlansGarlascoProduction(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed; run make rust-fetch")
	}
	path := filepath.Join("..", "..", "..", "..", "semantic_acceptance", "fixtures", "garlasco_2013_reconstructed.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("Garlasco fixture not available: %v", err)
	}
	var scenes []scriptgen.SemanticSceneInput
	for index, paragraph := range strings.Split(string(data), "\n\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		scenes = append(scenes, scriptgen.SemanticSceneInput{SceneID: fmt.Sprintf("s%02d", index+1), Text: paragraph})
	}
	adapter, err := NewSemanticEngineAdapter(NewSemanticEngineExecutor(binary, resolveFFmpegBinary(), zap.NewNop()))
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := adapter.SemanticBundles(context.Background(), "it", scenes)
	if err != nil {
		t.Fatalf("Garlasco semantic batch failed: %v", err)
	}
	feedback := scriptgen.ScriptSearchFeedbackFromBundles(bundles)
	if feedback == nil {
		t.Fatal("the real worker omitted search_feedback")
	}
	if len(feedback.Entries) != bundles["s01"].Consolidation.Inventory.SearchEntries {
		t.Fatalf("transport lost search entries: %d vs %d", len(feedback.Entries), bundles["s01"].Consolidation.Inventory.SearchEntries)
	}
	for _, entry := range feedback.Entries {
		if entry.Status != "pending" || len(entry.Queries) == 0 {
			t.Fatalf("engine feedback must stay pending with a ladder: %+v", entry)
		}
	}

	view, plan, plannerFeedback, err := scriptgen.PlanScriptProduction(bundles)
	if err != nil {
		t.Fatalf("planner rejected the consolidated view: %v", err)
	}
	if view == nil || plan == nil || plannerFeedback == nil {
		t.Fatal("the consolidated view produced no executable plan")
	}
	t.Logf("planner: raw=%d admitted=%d media=%d dropped_budget=%d dropped_scene=%d queries=%d identities=%d",
		plan.Metrics.RawCandidates, plan.Metrics.AdmittedUnits, plan.Metrics.MediaUnits,
		plan.Metrics.DroppedBudget, plan.Metrics.DroppedSceneBudget, plan.Metrics.PlannedQueries, len(plan.Identity))
	if plan.Metrics.MediaUnits == 0 || plan.Metrics.MediaUnits > 20 {
		t.Fatalf("the certified run budget was not applied: %+v", plan.Metrics)
	}
	activeLadders, queryRungs := 0, 0
	for _, unit := range plan.Units {
		if unit.RequiresMedia && unit.Status == scriptgen.ProductionUnitPlanned {
			if len(unit.Queries) == 0 {
				t.Fatalf("media unit %q lost its query ladder", unit.CandidateID)
			}
			activeLadders++
			queryRungs += len(unit.Queries)
		} else if len(unit.Queries) != 0 {
			t.Fatalf("unsearched unit %q retained a query ladder", unit.CandidateID)
		}
	}
	if activeLadders != plan.Metrics.MediaUnits || queryRungs != plan.Metrics.PlannedQueries {
		t.Fatalf("query ladder/rung accounting drift: ladders=%d rungs=%d metrics=%+v", activeLadders, queryRungs, plan.Metrics)
	}
	if plan.Metrics.AdmittedUnits < plan.Metrics.MediaUnits {
		t.Fatalf("every media unit is also an admitted unit: %+v", plan.Metrics)
	}
	admittedSurfaces := 0
	for _, identity := range plan.Identity {
		if identity.Media {
			admittedSurfaces++
			if identity.Surface == "" || identity.AssetKey == "" {
				t.Fatalf("an admitted identity must carry its surface and asset: %+v", identity)
			}
			if !plan.AdmitsIdentity(identity.Surface) {
				t.Fatalf("the plan must admit its own media identity: %+v", identity)
			}
		}
	}
	if admittedSurfaces == 0 {
		t.Fatal("no identity was admitted for an image")
	}

	attempt, ok := plan.NextSearchAttempt()
	if !ok || attempt.Query == "" || attempt.QueryKind != "exact_mention" {
		t.Fatalf("first planner attempt: %+v ok=%v", attempt, ok)
	}
	next, more, err := plan.RecordSearchOutcome(scriptgen.SemanticSearchOutcome{CandidateID: attempt.CandidateID, Rung: attempt.Rung})
	if err != nil {
		t.Fatalf("simulated first-rung failure: %v", err)
	}
	if more && next.CandidateID == attempt.CandidateID && next.Rung != attempt.Rung+1 {
		t.Fatalf("a failed rung must widen to the next one: %+v", next)
	}
	if more && next.CandidateID != attempt.CandidateID && next.Rung != 0 {
		t.Fatalf("a new composition starts at its first rung: %+v", next)
	}
	if _, more, err := plan.RecordSearchOutcome(scriptgen.SemanticSearchOutcome{CandidateID: next.CandidateID, Rung: next.Rung, Found: true, AssetID: "asset:simulated"}); err != nil || more != (plan.SearchableUnits() > 0) {
		t.Fatalf("a hit must settle the composition: more=%v err=%v", more, err)
	}
	plannerFeedback = plan.SearchFeedback()
	settled := 0
	for _, entry := range plannerFeedback.Entries {
		switch entry.Status {
		case "satisfied":
			settled++
		case "pending":
		case "failed":
			if entry.Retry == nil || entry.Retry.Action != "skip_visual" {
				t.Fatalf("a failed entry must report skip_visual: %+v", entry)
			}
		default:
			t.Fatalf("unexpected planner status %q in %+v", entry.Status, entry)
		}
	}
	if settled != 1 {
		t.Fatalf("exactly one composition must be satisfied after the hit: %+v", plannerFeedback.Entries)
	}

	// A provider that never resolves must end in skip_visual, never in a
	// silent retry loop.
	failed := 0
	for attempt, ok := plan.NextSearchAttempt(); ok; attempt, ok = plan.NextSearchAttempt() {
		if _, _, err := plan.RecordSearchOutcome(scriptgen.SemanticSearchOutcome{CandidateID: attempt.CandidateID, Rung: attempt.Rung, Error: "simulated provider miss"}); err != nil {
			t.Fatalf("simulated provider miss: %v", err)
		}
		if failed++; failed > 200 {
			t.Fatal("the search state machine did not terminate")
		}
	}
	for _, entry := range plan.SearchFeedback().Entries {
		if entry.Status == "failed" && (entry.Retry == nil || entry.Retry.Action != "skip_visual") {
			t.Fatalf("an exhausted search must report skip_visual: %+v", entry)
		}
	}
	if plan.Metrics.SearchesSatisfied != 1 || plan.Metrics.SearchesSkipped != plan.Metrics.SearchesFailed ||
		plan.Metrics.SearchesSatisfied+plan.Metrics.SearchesFailed != plan.Metrics.MediaUnits ||
		plan.SearchableUnits() != 0 || plan.Metrics.SearchAttempts > plan.Metrics.PlannedQueries {
		t.Fatalf("settled search metrics: %+v", plan.Metrics)
	}
	t.Logf("planner feedback: satisfied=%d failed=%d skipped=%d broadens=%d",
		plan.Metrics.SearchesSatisfied, plan.Metrics.SearchesFailed, plan.Metrics.SearchesSkipped, plan.Metrics.SearchBroadens)
}

// TestSemanticEngineRealBinaryPlansPlannerVolumes measures the planner-level
// reduction on the three real Garlasco scripts. The numbers it logs are the
// ones recorded in the acceptance report: manifest-level proposed work versus
// the compositions this run is actually allowed to produce.
func TestSemanticEngineRealBinaryPlansPlannerVolumes(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed; run make rust-fetch")
	}
	fixtures := []struct{ name, file string }{
		{"garlasco_2013_reconstructed", "garlasco_2013_reconstructed.txt"},
		{"garlasco_full", "garlasco_full.txt"},
		{"sempio_3k", "sempio_chiara_poggi_youtube_3k.txt"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "semantic_acceptance", "fixtures", fixture.file))
			if err != nil {
				t.Skipf("fixture not available: %v", err)
			}
			var scenes []scriptgen.SemanticSceneInput
			for index, paragraph := range strings.Split(string(data), "\n\n") {
				paragraph = strings.TrimSpace(paragraph)
				if paragraph == "" {
					continue
				}
				scenes = append(scenes, scriptgen.SemanticSceneInput{SceneID: fmt.Sprintf("s%02d", index+1), Text: paragraph})
			}
			adapter, err := NewSemanticEngineAdapter(NewSemanticEngineExecutor(binary, resolveFFmpegBinary(), zap.NewNop()))
			if err != nil {
				t.Fatal(err)
			}
			bundles, err := adapter.SemanticBundles(context.Background(), "it", scenes)
			if err != nil {
				t.Fatalf("semantic batch failed: %v", err)
			}
			view, plan, feedback, err := scriptgen.PlanScriptProduction(bundles)
			if err != nil || view == nil || plan == nil || feedback == nil {
				t.Fatalf("planner view failed: view=%v plan=%v feedback=%v err=%v", view, plan, feedback, err)
			}
			covered := plan.Coverage.CoveredFacts + plan.Coverage.DeferredFacts + plan.Coverage.UncoveredFacts
			if covered != plan.Coverage.CanonicalFacts {
				t.Fatalf("coverage accounting: %+v", plan.Coverage)
			}
			queryRungs := 0
			for _, unit := range plan.Units {
				queryRungs += len(unit.Queries)
			}
			if plan.Metrics.MediaUnits > 20 || plan.Metrics.PlannedQueries != queryRungs {
				t.Fatalf("run budget/query rung accounting: %+v, actual rungs=%d", plan.Metrics, queryRungs)
			}
			if len(feedback.Entries) != plan.Metrics.MediaUnits {
				t.Fatalf("one feedback entry per searched composition: %d vs %d", len(feedback.Entries), plan.Metrics.MediaUnits)
			}
			for _, identity := range plan.Identity {
				if identity.Media && (identity.AssetKey == "" || identity.Surface == "") {
					t.Fatalf("admitted identity incomplete: %+v", identity)
				}
			}
			t.Logf("%s: scenes=%d raw_proposals=%d raw_queries=%d raw_search_entries=%d production_queries=%d | candidates=%d admitted=%d media=%d dropped_scene=%d dropped_budget=%d planner_queries=%d identities=%d media_identities=%d | facts=%d covered=%d deferred=%d uncovered=%d",
				fixture.name, len(scenes), view.Metrics.RawProposals, view.Metrics.RawQueries, view.Metrics.RawSearchEntries,
				view.Metrics.ProductionQueries, plan.Metrics.RawCandidates, plan.Metrics.AdmittedUnits, plan.Metrics.MediaUnits,
				plan.Metrics.DroppedSceneBudget, plan.Metrics.DroppedBudget, plan.Metrics.PlannedQueries, len(plan.Identity), feedbackMediaIdentities(plan),
				plan.Coverage.CanonicalFacts, plan.Coverage.CoveredFacts, plan.Coverage.DeferredFacts, plan.Coverage.UncoveredFacts)
		})
	}
}

func feedbackMediaIdentities(plan *scriptgen.SemanticProductionPlan) int {
	count := 0
	for _, identity := range plan.Identity {
		if identity.Media {
			count++
		}
	}
	return count
}

func intPointer(value int) *int { return &value }

func TestSemanticEngineRealBinaryProjectsCrossSceneGroupsAgainstMemberSources(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed; run make rust-fetch")
	}
	inputs := []scriptgen.SemanticSceneInput{
		{SceneID: "compare-a", Text: "Lo Stadio Aurora è più capiente rispetto allo Stadio Olimpico."},
		{SceneID: "compare-b", Text: "Lo Stadio Aurora è più antico rispetto allo Stadio Olimpico."},
		{SceneID: "cause-a", Text: "Lo Stadio Aurora causa la crescita di Torino."},
		{SceneID: "cause-b", Text: "Torino causa la crescita di Milano."},
	}
	executor := NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := adapter.SemanticBundles(context.Background(), "it", inputs)
	if err != nil {
		t.Fatalf("real cross-scene semantic analysis failed: %v", err)
	}
	check := func(sceneID, basis string, minimumMembers int) {
		t.Helper()
		bundle := bundles[sceneID]
		if bundle == nil {
			t.Fatalf("scene %q missing from result", sceneID)
		}
		var found *scriptgen.SemanticGroup
		for i := range bundle.Groups {
			if bundle.Groups[i].RankingBasis == basis {
				found = &bundle.Groups[i]
				break
			}
		}
		if found == nil || len(found.Members) < minimumMembers {
			t.Fatalf("scene %q group basis %q missing or too small: %+v", sceneID, basis, bundle.Groups)
		}
		for _, member := range found.Members {
			var source string
			for _, input := range inputs {
				if input.SceneID == member.SceneID {
					source = input.Text
					break
				}
			}
			if source == "" || member.Start < 0 || member.End > len(source) ||
				member.Start >= member.End || source[member.Start:member.End] != member.Evidence || member.Text != member.Evidence {
				t.Fatalf("cross-scene member is not grounded against its own source: %+v", member)
			}
		}
	}
	check("compare-a", "explicit_pair_comparison_across_scenes", 4)
	check("cause-a", "explicit_asserted_causal_chain_across_scenes", 4)
}
