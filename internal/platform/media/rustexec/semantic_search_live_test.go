package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/pkg/providerbudget"
	"go.uber.org/zap"
)

// liveCommonsSearch probes the public Wikimedia Commons REST API through the
// same production port shape the batch planner uses. It is an acceptance
// probe, not a second production provider: it exists to measure hit rate and
// latency on the real plan against a real upstream.
type liveCommonsSearch struct {
	client *http.Client
	// supportsPerson models a Commons-only lane. Commons has no encyclopedic
	// asset for a person's proper name, so the routed probe declares the class
	// unsupported instead of spending a permit on a query that cannot hit.
	supportsPerson bool
}

func (p *liveCommonsSearch) SupportsClass(class scriptgen.SemanticSearchClass) bool {
	if class == scriptgen.SearchClassPerson {
		return p.supportsPerson
	}
	return true
}

func (p *liveCommonsSearch) Search(ctx context.Context, req scriptgen.SemanticAssetSearchRequest) (scriptgen.SemanticAssetSearchResponse, error) {
	if req.QueryClass == string(scriptgen.SearchClassPerson) && !p.supportsPerson {
		return scriptgen.SemanticAssetSearchResponse{}, fmt.Errorf("commons lane does not serve person queries")
	}
	endpoint := "https://api.wikimedia.org/core/v1/commons/search/page?q=" + url.QueryEscape(req.Query) + "&limit=8"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return scriptgen.SemanticAssetSearchResponse{}, err
	}
	httpReq.Header.Set("Api-User-Agent", "PipelineGen-Semantic-Search-Probe/1.0")
	httpReq.Header.Set("User-Agent", "PipelineGen-Semantic-Search-Probe/1.0 (https://pipelinegen.invalid; probe)")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return scriptgen.SemanticAssetSearchResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return scriptgen.SemanticAssetSearchResponse{}, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return scriptgen.SemanticAssetSearchResponse{}, &providerbudget.RateLimited{
			Provider: "wikimedia_commons", RetryAfter: retryAfterHeader(resp),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return scriptgen.SemanticAssetSearchResponse{}, fmt.Errorf("commons HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Pages []struct {
			Key string `json:"key"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return scriptgen.SemanticAssetSearchResponse{}, err
	}
	response := scriptgen.SemanticAssetSearchResponse{Provider: "wikimedia_commons_live"}
	for _, page := range payload.Pages {
		lower := strings.ToLower(page.Key)
		if !strings.HasPrefix(page.Key, "File:") {
			continue
		}
		if !(strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") ||
			strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".webp")) {
			continue
		}
		response.Hits = append(response.Hits, scriptgen.SemanticAssetHit{
			AssetID: page.Key, URL: "https://commons.wikimedia.org/wiki/" + url.PathEscape(page.Key),
		})
	}
	return response, nil
}

func retryAfterHeader(resp *http.Response) time.Duration {
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		if wait := time.Until(when); wait > 0 {
			return wait
		}
	}
	return 30 * time.Second
}

// liveGarlascoPlan runs the real engine and projects the certified production
// plan. Each measurement gets a fresh plan: the planner mutates the units it
// settles, so re-running the same value would measure an already-finished plan.
func liveGarlascoPlan(t *testing.T) *scriptgen.SemanticProductionPlan {
	t.Helper()
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
	_, plan, _, err := scriptgen.PlanScriptProduction(bundles)
	if err != nil || plan == nil {
		t.Fatalf("plan build failed: plan=%v err=%v", plan, err)
	}
	return plan
}

func liveCommonsBudget() *providerbudget.Registry {
	registry := providerbudget.New(providerbudget.Config{Interval: 250 * time.Millisecond, Burst: 1, Cooldown: 30 * time.Second})
	registry.Configure("wikimedia_commons", providerbudget.Config{Interval: 250 * time.Millisecond, Burst: 1, Cooldown: 30 * time.Second})
	return registry
}

func livePlanner(t *testing.T, port scriptgen.SemanticAssetSearchPort, cache bool) *scriptgen.SemanticSearchBatchPlanner {
	t.Helper()
	config := scriptgen.SemanticSearchBatchConfig{
		Limit: 8, QueryTimeout: 12 * time.Second, MaxQueries: 64,
		MaxConcurrency: 4, RateLimitRetries: 2, Budget: liveCommonsBudget(),
		DisableCache: !cache,
	}
	planner, err := scriptgen.NewSemanticSearchBatchPlanner(port, config)
	if err != nil {
		t.Fatal(err)
	}
	return planner
}

func logLiveReport(t *testing.T, label string, report *scriptgen.SemanticSearchBatchReport) {
	t.Helper()
	t.Logf("%s: planned=%d queries=%d hits=%d misses=%d errors=%d hit_rate=%.3f route_skips=%d cache_hits=%d provider_queries=%d rate_limited=%d p50=%dms p95=%dms total=%dms",
		label, report.Metrics.PlannedSearches, report.Metrics.Queries, report.Metrics.Hits, report.Metrics.Misses,
		report.Metrics.Errors, report.Metrics.HitRate, report.Metrics.RouteSkips, report.Metrics.CacheHits,
		report.Metrics.ProviderQueries, report.Metrics.RateLimitRetries, report.Metrics.P50MS, report.Metrics.P95MS, report.Metrics.TotalMS)
	for _, attempt := range report.Attempts {
		t.Logf("  %s rung=%d class=%-12s %-55q status=%-8s cached=%t hits=%d latency=%dms err=%q",
			attempt.CandidateID, attempt.Rung, attempt.Class, attempt.Query, attempt.Status,
			attempt.Cached, attempt.Hits, attempt.LatencyMS, attempt.Error)
	}
}

// TestSemanticSearchLiveProviderMeasuresHitRateAndLatency is the real-upstream
// acceptance probe. It is opt-in (VELOX_SEMANTIC_SEARCH_LIVE=1) because it
// needs network access and a public API. It measures three configurations on
// the same 18-query Garlasco plan:
//
//  1. baseline: every query goes to Commons, deterministic 250ms pacing;
//  2. routing: person queries are not sent to Commons at all;
//  3. warm: the routed plan runs again with the same planner cache.
func TestSemanticSearchLiveProviderMeasuresHitRateAndLatency(t *testing.T) {
	if os.Getenv("VELOX_SEMANTIC_SEARCH_LIVE") != "1" {
		t.Skip("set VELOX_SEMANTIC_SEARCH_LIVE=1 to run the live provider measurement")
	}
	client := &http.Client{Timeout: 15 * time.Second}

	baselinePlanner := livePlanner(t, &liveCommonsSearch{client: client, supportsPerson: true}, false)
	baseline, err := baselinePlanner.Run(context.Background(), liveGarlascoPlan(t), "it")
	if err != nil {
		t.Fatalf("baseline live batch failed: %v", err)
	}
	logLiveReport(t, "baseline (Commons for all 18 queries, no cache)", baseline)

	routingPort := &liveCommonsSearch{client: client, supportsPerson: false}
	routingPlanner := livePlanner(t, routingPort, true)
	routing, err := routingPlanner.Run(context.Background(), liveGarlascoPlan(t), "it")
	if err != nil {
		t.Fatalf("routed live batch failed: %v", err)
	}
	logLiveReport(t, "routed (person queries not sent to Commons)", routing)

	warm, err := routingPlanner.Run(context.Background(), liveGarlascoPlan(t), "it")
	if err != nil {
		t.Fatalf("warm live batch failed: %v", err)
	}
	logLiveReport(t, "warm (same planner cache, fresh plan)", warm)

	if baseline.Metrics.Queries == 0 {
		t.Fatal("the baseline probe executed no query")
	}
	if baseline.Metrics.RouteSkips != 0 {
		t.Fatalf("the baseline port must support every class, got %d route skips", baseline.Metrics.RouteSkips)
	}
	if routing.Metrics.RouteSkips == 0 {
		t.Fatal("routing produced no route skip: person queries still reached Commons")
	}
	if routing.Metrics.Queries+routing.Metrics.RouteSkips != baseline.Metrics.Queries {
		t.Fatalf("routed batch lost queries: routed=%d + skipped=%d, baseline=%d",
			routing.Metrics.Queries, routing.Metrics.RouteSkips, baseline.Metrics.Queries)
	}
	if routing.Metrics.Hits != baseline.Metrics.Hits {
		t.Fatalf("routing changed the hit count: baseline=%d routed=%d", baseline.Metrics.Hits, routing.Metrics.Hits)
	}
	if warm.Metrics.CacheHits < routing.Metrics.Hits {
		t.Fatalf("warm run cache hits = %d, want at least the %d prior hits", warm.Metrics.CacheHits, routing.Metrics.Hits)
	}
	if warm.Metrics.ProviderQueries >= routing.Metrics.ProviderQueries {
		t.Fatalf("warm run provider queries = %d, want fewer than the cold routed run's %d",
			warm.Metrics.ProviderQueries, routing.Metrics.ProviderQueries)
	}
	if warm.Metrics.TotalMS >= routing.Metrics.TotalMS {
		t.Fatalf("warm run total = %dms, want less than cold %dms", warm.Metrics.TotalMS, routing.Metrics.TotalMS)
	}
	for _, attempt := range routing.Attempts {
		if attempt.Status == "error" && strings.Contains(attempt.Error, "429") {
			t.Fatalf("routed run hit an unhandled 429: %+v", attempt)
		}
	}
	if routing.Metrics.HitRate < 0 || routing.Metrics.HitRate > 1 {
		t.Fatalf("hit rate out of range: %+v", routing.Metrics)
	}
	if routing.Metrics.TotalMS <= 0 || routing.Metrics.P95MS < routing.Metrics.P50MS {
		t.Fatalf("latency measurement is inconsistent: %+v", routing.Metrics)
	}
}
