package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	imagesapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// liveEmptyImageRepo keeps the resolver hermetic: this probe asks the live
// providers every time and never reuses a persisted image.
type liveEmptyImageRepo struct{}

func (liveEmptyImageRepo) ListImages(context.Context, imagesapp.ImageFilter) ([]imagesapp.ImageSearchResult, error) {
	return nil, nil
}

// TestLiveSemanticProductionBlendRoutesRealProviders is the "real job"
// verification of the semantic production planner: it builds the production
// plan from the Garlasco acceptance script with the real Rust semantic engine,
// then executes the plan through the SAME chain production wires
// (internetImageSearchAdapter → ImageSearchResolver → retrieval provider
// registry → DDG/Commons/SearXNG), with the durable SQLite plan cache, twice:
// a cold run and a warm run on a rebuilt plan.
//
// It is opt-in because it calls the public image providers:
//
//	VELOX_SEMANTIC_BLEND_LIVE=1 go test ./internal/app/wiring -run TestLiveSemanticProductionBlendRoutesRealProviders -v -count=1
func TestLiveSemanticProductionBlendRoutesRealProviders(t *testing.T) {
	if os.Getenv("VELOX_SEMANTIC_BLEND_LIVE") != "1" {
		t.Skip("set VELOX_SEMANTIC_BLEND_LIVE=1 to run the live provider blend")
	}
	root := filepath.Join("..", "..", "..")
	binary := os.Getenv("VELOX_RUST_SEMANTIC_ENGINE_PATH")
	if binary == "" {
		binary = filepath.Join(root, "bin", "semantic-engine")
	}
	if info, err := os.Stat(binary); err != nil || info.Mode()&0o111 == 0 {
		t.Skipf("semantic-engine binary not installed at %s; run make rust-fetch", binary)
	}
	fixture := filepath.Join(root, "semantic_acceptance", "fixtures", "garlasco_2013_reconstructed.txt")
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Skipf("acceptance fixture not available: %v", err)
	}

	log := zap.NewNop()
	scenes := make([]scriptgen.SemanticSceneInput, 0, 16)
	for index, paragraph := range strings.Split(string(data), "\n\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		scenes = append(scenes, scriptgen.SemanticSceneInput{SceneID: fmt.Sprintf("s%02d", index+1), Text: paragraph})
	}
	engine, err := rustexec.NewSemanticEngineAdapter(rustexec.NewSemanticEngineExecutor(binary, "", log))
	if err != nil {
		t.Fatalf("semantic engine adapter: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	bundles, err := engine.SemanticBundles(ctx, "it", scenes)
	if err != nil {
		t.Fatalf("real semantic analysis failed: %v", err)
	}
	_, coldPlan, _, err := scriptgen.PlanScriptProduction(bundles)
	if err != nil {
		t.Fatalf("production plan failed: %v", err)
	}

	// The exact production chain used by script_generation_runtime.go.
	cfg := &config.Config{}
	cfg.External.SearxngURL = strings.TrimSpace(os.Getenv("SEARXNG_URL"))
	if cfg.External.SearxngURL == "" {
		cfg.External.SearxngURL = "http://127.0.0.1:8080"
	}
	imageService := imagesapp.NewService(imagesapp.ImagesDeps{
		Core:     imagesapp.ImagesCoreDeps{Cfg: cfg, Log: log},
		External: imagesapp.ImagesExternalDeps{RemoteFetch: &http.Client{Timeout: 20 * time.Second}},
	})
	resolver, err := imagesapp.NewImageSearchResolver(
		imagesapp.WithRetrievalBackend(imageService.RetrievalRegistry()),
		imagesapp.WithImageListRepository(liveEmptyImageRepo{}),
	)
	if err != nil {
		t.Fatalf("image search resolver: %v", err)
	}
	searcher := newInternetImageSearchAdapter(resolver, log)

	cacheDB, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "plan-cache.db"))
	if err != nil {
		t.Fatalf("open cache db: %v", err)
	}
	defer func() { _ = cacheDB.Close() }()
	if _, err := cacheDB.Exec(`CREATE TABLE semantic_plan_cache (
		cache_key TEXT PRIMARY KEY, payload_json TEXT NOT NULL,
		provider TEXT NOT NULL DEFAULT '', provider_lane TEXT NOT NULL DEFAULT '',
		stored_at TEXT NOT NULL, expires_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create cache schema: %v", err)
	}

	batchConfig := scriptgen.SemanticSearchBatchConfig{
		Limit: 5, MaxConcurrency: 4, QueryTimeout: 25 * time.Second,
		Cache:    sqlitescripts.NewSQLiteSemanticPlanCacheAdapter(cacheDB, time.Hour, log),
		CacheTTL: time.Hour,
	}
	planner, err := scriptgen.NewSemanticSearchBatchPlanner(documentadapters.NewSemanticAssetSearch(searcher), batchConfig)
	if err != nil {
		t.Fatalf("planner: %v", err)
	}
	coldReport, err := planner.Run(ctx, coldPlan, "it")
	if err != nil {
		t.Fatalf("cold plan run: %v", err)
	}
	if coldReport.Metrics.Queries == 0 {
		t.Skip("the production plan produced no searchable composition on this fixture")
	}
	logBlend(t, "cold", coldReport)
	assertPersonRoutesNeverHitCommons(t, coldReport)

	// Rebuild the plan (the first run mutated its units) and reuse the same
	// durable cache: the warm pass must reuse provider answers.
	_, warmPlan, _, err := scriptgen.PlanScriptProduction(bundles)
	if err != nil {
		t.Fatalf("rebuild production plan: %v", err)
	}
	warmReport, err := planner.Run(ctx, warmPlan, "it")
	if err != nil {
		t.Fatalf("warm plan run: %v", err)
	}
	logBlend(t, "warm", warmReport)
	if warmReport.Metrics.CacheHits == 0 {
		t.Fatalf("warm run served no cache hit: %+v", warmReport.Metrics)
	}
	if warmReport.Metrics.ProviderQueries >= coldReport.Metrics.Queries {
		t.Fatalf("warm run did not reduce provider calls: cold=%d warm=%d",
			coldReport.Metrics.Queries, warmReport.Metrics.ProviderQueries)
	}
}

// logBlend prints the measured provider blend: which upstream answered each
// query class, and the per-provider totals an operator can compare with the
// acceptance report.
func logBlend(t *testing.T, label string, report *scriptgen.SemanticSearchBatchReport) {
	t.Helper()
	served := map[string]int{}
	attempted := map[scriptgen.SemanticSearchClass]int{}
	for _, row := range report.Attempts {
		attempted[row.Class]++
		if row.Provider != "" {
			served[row.Provider]++
		}
		t.Logf("%s class=%s entity=%q query=%q status=%s provider=%q hits=%d latency_ms=%d cached=%t",
			label, row.Class, row.Entity, row.Query, row.Status, row.Provider, row.Hits, row.LatencyMS, row.Cached)
	}
	providers := make([]string, 0, len(served))
	for provider := range served {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	t.Logf("%s metrics: planned=%d queries=%d hits=%d misses=%d errors=%d route_skips=%d cache_hits=%d provider_queries=%d p50_ms=%d p95_ms=%d total_ms=%d",
		label, report.Metrics.PlannedSearches, report.Metrics.Queries, report.Metrics.Hits, report.Metrics.Misses,
		report.Metrics.Errors, report.Metrics.RouteSkips, report.Metrics.CacheHits, report.Metrics.ProviderQueries,
		report.Metrics.P50MS, report.Metrics.P95MS, report.Metrics.TotalMS)
	for _, provider := range providers {
		t.Logf("%s provider=%s serving_queries=%d", label, provider, served[provider])
	}
	for class, count := range attempted {
		t.Logf("%s class=%s attempts=%d", label, class, count)
	}
	if len(providers) == 0 {
		t.Logf("%s: no provider produced a hit; check network access and SEARXNG_URL", label)
	}
}

// assertPersonRoutesNeverHitCommons pins the routing contract: a proper name of
// a person is never sent to Wikimedia Commons, whose index cannot answer it.
func assertPersonRoutesNeverHitCommons(t *testing.T, report *scriptgen.SemanticSearchBatchReport) {
	t.Helper()
	for _, row := range report.Attempts {
		if row.Class == scriptgen.SearchClassPerson && row.Provider == "wikimedia_commons" {
			t.Fatalf("person query %q was answered by Wikimedia Commons; routing regressed", row.Query)
		}
	}
}
