package scriptgeneration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type batchTestClock struct {
	now time.Time
}

func (c *batchTestClock) Now() time.Time { return c.now }
func (c *batchTestClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

type batchTestPort struct {
	clock   *batchTestClock
	results map[string][]SemanticAssetHit
	errors  map[string]error
	delays  map[string]time.Duration
	calls   []SemanticAssetSearchRequest
}

func (p *batchTestPort) Search(_ context.Context, req SemanticAssetSearchRequest) (SemanticAssetSearchResponse, error) {
	p.calls = append(p.calls, req)
	if delay := p.delays[req.Query]; delay > 0 {
		p.clock.Advance(delay)
	}
	if err := p.errors[req.Query]; err != nil {
		return SemanticAssetSearchResponse{}, err
	}
	hits := p.results[req.Query]
	return SemanticAssetSearchResponse{Hits: hits, Provider: "fake-provider"}, nil
}

func batchPlan(t *testing.T, candidates ...SemanticProductionCandidate) (*SemanticProductionPlan, *batchTestClock) {
	t.Helper()
	plan, err := BuildSemanticProductionPlan(planConsolidation(candidates, nil), planIdentities(), DefaultSemanticProductionPlanConfig())
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	return plan, &batchTestClock{now: time.Unix(100, 0)}
}

func batchPlanner(t *testing.T, port *batchTestPort, clock *batchTestClock, config ...SemanticSearchBatchConfig) *SemanticSearchBatchPlanner {
	t.Helper()
	cfg := SemanticSearchBatchConfig{MaxConcurrency: 1, DisableCache: true}
	if len(config) > 0 {
		cfg = config[0]
		if cfg.MaxConcurrency == 0 {
			cfg.MaxConcurrency = 1
		}
	}
	planner, err := NewSemanticSearchBatchPlanner(port, cfg)
	if err != nil {
		t.Fatalf("planner build failed: %v", err)
	}
	planner.now = clock.Now
	return planner
}

func TestSemanticSearchBatchMeasuresRealProviderOutcomes(t *testing.T) {
	exact := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	sentence := SemanticSearchQuery{Kind: "source_sentence", Text: "Alberto Stasi nel procedimento."}
	plan, clock := batchPlan(t,
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", exact, sentence),
	)
	port := &batchTestPort{
		clock: clock,
		results: map[string][]SemanticAssetHit{
			sentence.Text: {{AssetID: "asset_42", URL: "https://assets.invalid/42.jpg", SHA256: strings.Repeat("a", 64)}},
		},
		delays: map[string]time.Duration{exact.Text: 10 * time.Millisecond, sentence.Text: 40 * time.Millisecond},
	}
	report, err := batchPlanner(t, port, clock).Run(context.Background(), plan, "it")
	if err != nil {
		t.Fatalf("batch failed: %v", err)
	}
	if len(port.calls) != 2 || port.calls[0].QueryKind != "exact_mention" || port.calls[1].QueryKind != "source_sentence" {
		t.Fatalf("the batch must walk the widen ladder: %+v", port.calls)
	}
	if port.calls[0].Language != "it" || port.calls[0].Limit <= 0 || port.calls[0].CandidateID != "vc_0001" {
		t.Fatalf("request projection: %+v", port.calls[0])
	}
	if report.Schema != SemanticSearchBatchSchema || len(report.Attempts) != 2 {
		t.Fatalf("report shape: %+v", report)
	}
	first, second := report.Attempts[0], report.Attempts[1]
	if first.Status != "miss" || first.RetryAction != "broaden" || first.LatencyMS != 10 || first.Rung != 0 {
		t.Fatalf("first attempt: %+v", first)
	}
	if second.Status != "hit" || second.RetryAction != "" || second.LatencyMS != 40 || second.Hits != 1 {
		t.Fatalf("second attempt: %+v", second)
	}
	if len(second.AssetIDs) != 1 || second.AssetIDs[0] != "asset_42" || second.Provider != "fake-provider" {
		t.Fatalf("hit projection: %+v", second)
	}
	metrics := report.Metrics
	if metrics.Queries != 2 || metrics.Hits != 1 || metrics.Misses != 1 || metrics.HitRate != 0.5 ||
		metrics.MeanMS != 25 || metrics.P50MS != 10 || metrics.P95MS != 40 || metrics.TotalMS != 50 {
		t.Fatalf("measured metrics: %+v", metrics)
	}
	if report.Feedback == nil || len(report.Feedback.Entries) != 1 || report.Feedback.Entries[0].Status != "satisfied" {
		t.Fatalf("feedback after the batch: %+v", report.Feedback)
	}
	unit := plan.Units[0]
	if unit.SearchStatus != ProductionSearchSatisfied || unit.ResolvedAssetID != "asset_42" {
		t.Fatalf("the plan must record the resolved asset: %+v", unit)
	}
}

func TestSemanticSearchBatchTreatsProviderErrorsAsMissesAndSkips(t *testing.T) {
	exact := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	plan, clock := batchPlan(t,
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", exact),
	)
	port := &batchTestPort{clock: clock, errors: map[string]error{exact.Text: errors.New("provider exploded")}}
	report, err := batchPlanner(t, port, clock).Run(context.Background(), plan, "it")
	if err != nil {
		t.Fatalf("a provider outage must not fail the batch: %v", err)
	}
	if len(report.Attempts) != 1 || report.Attempts[0].Status != "error" || !strings.Contains(report.Attempts[0].Error, "provider exploded") {
		t.Fatalf("error attempt: %+v", report.Attempts)
	}
	if report.Attempts[0].RetryAction != "skip_visual" {
		t.Fatalf("an unrecoverable error must skip the visual: %+v", report.Attempts[0])
	}
	if report.Metrics.Errors != 1 || report.Metrics.Skipped != 1 || report.Metrics.HitRate != 0 {
		t.Fatalf("error metrics: %+v", report.Metrics)
	}
	if report.Feedback == nil || len(report.Feedback.Entries) != 1 || report.Feedback.Entries[0].Status != "failed" {
		t.Fatalf("feedback must report the failure: %+v", report.Feedback)
	}
	if plan.AdmitsIdentity("Alberto Stasi") {
		t.Fatal("a skipped visual loses its image admission")
	}
}

func TestSemanticSearchBatchHonorsTheQueryBudget(t *testing.T) {
	exact := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	sentence := SemanticSearchQuery{Kind: "source_sentence", Text: "Alberto Stasi nel procedimento."}
	plan, clock := batchPlan(t,
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", exact, sentence),
	)
	port := &batchTestPort{clock: clock}
	report, err := batchPlanner(t, port, clock, SemanticSearchBatchConfig{MaxQueries: 1}).Run(context.Background(), plan, "it")
	if err == nil || !strings.Contains(err.Error(), "query budget") {
		t.Fatalf("the safety bound must abort, got %v", err)
	}
	if report == nil || report.AbortedError == "" || len(report.Attempts) != 1 {
		t.Fatalf("aborted report: %+v", report)
	}
}

func TestSemanticSearchBatchStopsOnCancellation(t *testing.T) {
	exact := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	plan, clock := batchPlan(t,
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", exact),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := batchPlanner(t, &batchTestPort{clock: clock}, clock).Run(ctx, plan, "it")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation must propagate, got %v", err)
	}
	if report == nil || report.AbortedError == "" || len(report.Attempts) != 0 {
		t.Fatalf("cancelled report: %+v", report)
	}
}

func TestSemanticSearchBatchRequiresAPortAndAPlan(t *testing.T) {
	if _, err := NewSemanticSearchBatchPlanner(nil); err == nil {
		t.Fatal("a nil port must be rejected")
	}
	planner, err := NewSemanticSearchBatchPlanner(&batchTestPort{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Run(context.Background(), nil, "it"); err == nil {
		t.Fatal("a nil plan must be rejected")
	}
}

func TestSemanticSearchBatchWithNothingToSearchIsANoop(t *testing.T) {
	plan, clock := batchPlan(t, planCandidate("vc_0001", "timeline", "s01", 1, nil, "timeline:g1"))
	report, err := batchPlanner(t, &batchTestPort{clock: clock}, clock).Run(context.Background(), plan, "it")
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || len(report.Attempts) != 0 || report.Metrics.PlannedSearches != 0 {
		t.Fatalf("empty plan report: %+v", report)
	}
}

// TestSemanticSearchBatchResolvedHitBecomesAnOverlayBinding closes the loop
// between the measured batch and the rendered overlay: the hit's content
// address must survive the batch, and the plan must be able to bind it to the
// scene entity it belongs to.
func TestSemanticSearchBatchResolvedHitBecomesAnOverlayBinding(t *testing.T) {
	exact := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	plan, clock := batchPlan(t,
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", exact),
	)
	hash := strings.Repeat("c", 64)
	port := &batchTestPort{clock: clock, results: map[string][]SemanticAssetHit{
		exact.Text: {{
			AssetID: "asset-durable", URL: "https://upload.wikimedia.org/stasi.jpg", SHA256: hash,
			DriveLink: "https://drive.invalid/stasi", MediaType: "image/jpeg", Provider: "wikimedia_commons", Width: 1200, Height: 900,
		}},
	}}
	result := &GenerateResult{
		Scenes: []Scene{{ID: "s01", Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			Text: "Alberto Stasi", CanonicalName: "Alberto Stasi", Type: "PERSON",
		}}}}},
		SemanticProductionPlan: plan,
	}
	runner := &Runner{semanticSearchPlanner: batchPlanner(t, port, clock)}
	runner.executeSemanticSearchBatch(context.Background(), "it", result)

	unit := plan.Units[0]
	if unit.ResolvedImage == nil || unit.ResolvedImage.SHA256 != hash || unit.ResolvedImage.Provider != "wikimedia_commons" {
		t.Fatalf("the batch must carry the hit's content address and provider: %+v", unit.ResolvedImage)
	}
	if applied := ApplySemanticResolvedImages(result); applied != 1 {
		t.Fatalf("applied bindings = %d, want 1", applied)
	}
	binding := result.Scenes[0].Annotations.PrimaryEntities[0].Image
	if binding == nil || binding.SHA256 != hash || binding.PreviewURL != "https://upload.wikimedia.org/stasi.jpg" {
		t.Fatalf("overlay binding = %+v, want the resolved plan image", binding)
	}
}

func TestRunnerExecutesTheSemanticSearchBatch(t *testing.T) {
	exact := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	plan, clock := batchPlan(t,
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", exact),
	)
	port := &batchTestPort{clock: clock, results: map[string][]SemanticAssetHit{
		exact.Text: {{AssetID: "asset_7", URL: "https://assets.invalid/7.jpg"}},
	}}
	runner := &Runner{semanticSearchPlanner: batchPlanner(t, port, clock)}
	result := &GenerateResult{SemanticProductionPlan: plan, SemanticSearchFeedback: plan.SearchFeedback()}
	runner.executeSemanticSearchBatch(context.Background(), "it", result)
	if result.SemanticSearchReport == nil || result.SemanticSearchReport.Metrics.Hits != 1 {
		t.Fatalf("the runner must publish the measured report: %+v", result.SemanticSearchReport)
	}
	if result.SemanticSearchFeedback == nil || result.SemanticSearchFeedback.Entries[0].Status != "satisfied" {
		t.Fatalf("the runner must refresh the feedback: %+v", result.SemanticSearchFeedback)
	}
	if len(port.calls) != 1 {
		t.Fatalf("provider calls: %+v", port.calls)
	}

	offline := &GenerateResult{SemanticProductionPlan: plan}
	(&Runner{}).executeSemanticSearchBatch(context.Background(), "it", offline)
	if offline.SemanticSearchReport != nil {
		t.Fatal("an unwired planner must leave the result untouched")
	}
}
