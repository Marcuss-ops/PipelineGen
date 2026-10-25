package scriptgeneration

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/providerbudget"
)

type schedulerTestPort struct {
	mu        sync.Mutex
	calls     map[string][]int
	active    int
	maxActive int
	delay     time.Duration
	hits      map[string][]SemanticAssetHit
	limited   map[string]int
}

func newSchedulerTestPort() *schedulerTestPort {
	return &schedulerTestPort{calls: map[string][]int{}, hits: map[string][]SemanticAssetHit{}, limited: map[string]int{}}
}

func (p *schedulerTestPort) Search(ctx context.Context, req SemanticAssetSearchRequest) (SemanticAssetSearchResponse, error) {
	p.mu.Lock()
	p.calls[req.CandidateID] = append(p.calls[req.CandidateID], req.Rung)
	p.active++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	remaining := p.limited[req.Query]
	if remaining > 0 {
		p.limited[req.Query] = remaining - 1
	}
	p.mu.Unlock()

	if p.delay > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(p.delay):
		}
	}

	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	if remaining > 0 {
		return SemanticAssetSearchResponse{}, &providerbudget.RateLimited{Provider: "test", RetryAfter: 15 * time.Millisecond}
	}
	return SemanticAssetSearchResponse{Hits: p.hits[req.Query], Provider: "test-provider"}, nil
}

func schedulerPlan(t *testing.T, identities []SemanticIdentity, candidates ...SemanticProductionCandidate) *SemanticProductionPlan {
	t.Helper()
	plan, err := BuildSemanticProductionPlan(planConsolidation(candidates, nil), identities, DefaultSemanticProductionPlanConfig())
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	return plan
}

func schedulerIdentities() []SemanticIdentity {
	return []SemanticIdentity{
		{ID: "person_01", CanonicalSurface: "Alberto Stasi", Kind: "PERSON"},
		{ID: "person_02", CanonicalSurface: "Andrea Sempio", Kind: "PERSON"},
		{ID: "org_01", CanonicalSurface: "Corte d'Appello", Kind: "ORG"},
		{ID: "loc_01", CanonicalSurface: "Vigevano", Kind: "LOCATION"},
	}
}

func TestSemanticSearchClassesRouteToTheLaneThatCanAnswer(t *testing.T) {
	cases := []struct {
		kind  string
		class SemanticSearchClass
		lane  string
	}{
		{"PERSON", SearchClassPerson, "duckduckgo"},
		{"ORG", SearchClassOrganization, "wikimedia_commons"},
		{"LOCATION", SearchClassLocation, "wikimedia_commons"},
		{"OBJECT", SearchClassObject, "wikimedia_commons"},
		{"EVENT", SearchClassEvent, "generic"},
		{"UNKNOWN", SearchClassGeneric, "generic"},
	}
	for _, tc := range cases {
		class := SearchClassForIdentityKind(tc.kind)
		if class != tc.class || class.ProviderLane() != tc.lane {
			t.Fatalf("kind %q → class %q lane %q, want %q/%q", tc.kind, class, class.ProviderLane(), tc.class, tc.lane)
		}
	}
	if got := SearchClassFromQuery("Corte d'Appello di Brescia"); got != SearchClassOrganization {
		t.Fatalf("institutional query class = %q, want organization", got)
	}
	if got := SearchClassFromQuery("un bosco di notte"); got != SearchClassGeneric {
		t.Fatalf("free-text query class = %q, want generic", got)
	}
}

func TestPendingSearchAttemptsCarryRankEntityAndClass(t *testing.T) {
	plan := schedulerPlan(t, schedulerIdentities(), planCandidate(
		"vc_0001", "entity_search", "s01", 1, []string{"org_01"}, "entity:org_01",
		SemanticSearchQuery{Kind: "exact_mention", Text: "Corte d'Appello"},
	))
	attempts := plan.PendingSearchAttempts()
	if len(attempts) != 1 {
		t.Fatalf("pending attempts = %+v", attempts)
	}
	got := attempts[0]
	if got.Rank != 1 || got.SceneID != "s01" || got.Entity != "Corte d'Appello" || got.Class != SearchClassOrganization {
		t.Fatalf("attempt projection = %+v", got)
	}
}

func TestPendingSearchAttemptsClassifyUntypedInstitutionalQuery(t *testing.T) {
	identities := []SemanticIdentity{{ID: "entity_01", CanonicalSurface: "Corte d'Appello", Kind: ""}}
	plan := schedulerPlan(t, identities, planCandidate(
		"vc_0001", "entity_search", "s01", 1, []string{"entity_01"}, "entity:entity_01",
		SemanticSearchQuery{Kind: "exact_mention", Text: "Corte d'Appello"},
	))
	attempt, ok := plan.NextSearchAttempt()
	if !ok || attempt.Class != SearchClassOrganization {
		t.Fatalf("untyped institutional attempt = %+v ok=%v, want organization", attempt, ok)
	}
}

func TestSemanticSearchBatchRunsIndependentLaddersConcurrently(t *testing.T) {
	stasi := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	sempio := SemanticSearchQuery{Kind: "exact_mention", Text: "Andrea Sempio"}
	corte := SemanticSearchQuery{Kind: "exact_mention", Text: "Corte d'Appello"}
	vigevano := SemanticSearchQuery{Kind: "exact_mention", Text: "Vigevano"}
	sentence := func(text string) SemanticSearchQuery {
		return SemanticSearchQuery{Kind: "source_sentence", Text: text + " nel procedimento."}
	}
	plan := schedulerPlan(t, schedulerIdentities(),
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", stasi, sentence("Alberto Stasi")),
		planCandidate("vc_0002", "entity_search", "s02", 2, []string{"person_02"}, "entity:person_02", sempio, sentence("Andrea Sempio")),
		planCandidate("vc_0003", "entity_search", "s03", 3, []string{"org_01"}, "entity:org_01", corte, sentence("Corte d'Appello")),
		planCandidate("vc_0004", "entity_search", "s04", 4, []string{"loc_01"}, "entity:loc_01", vigevano, sentence("Vigevano")),
	)
	port := newSchedulerTestPort()
	port.delay = 30 * time.Millisecond
	port.hits = map[string][]SemanticAssetHit{
		sentence("Alberto Stasi").Text:   {{AssetID: "a1", SHA256: strings.Repeat("a", 64)}},
		sentence("Andrea Sempio").Text:   {{AssetID: "a2", SHA256: strings.Repeat("b", 64)}},
		sentence("Corte d'Appello").Text: {{AssetID: "a3", SHA256: strings.Repeat("c", 64)}},
		sentence("Vigevano").Text:        {{AssetID: "a4", SHA256: strings.Repeat("d", 64)}},
	}
	budget := providerbudget.New(providerbudget.Config{Interval: 0, Burst: 8, Cooldown: time.Second})
	planner, err := NewSemanticSearchBatchPlanner(port, SemanticSearchBatchConfig{
		MaxConcurrency: 4, DisableCache: true, Budget: budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := planner.Run(context.Background(), plan, "it")
	if err != nil {
		t.Fatalf("batch failed: %v", err)
	}
	if port.maxActive < 2 {
		t.Fatalf("independent ladders never overlapped: max active = %d", port.maxActive)
	}
	if report.Metrics.Queries != 8 || report.Metrics.Hits != 4 || report.Metrics.Misses != 4 {
		t.Fatalf("metrics = %+v, want 8/4/4", report.Metrics)
	}
	for candidate, rungs := range port.calls {
		if len(rungs) != 2 || rungs[0] != 0 || rungs[1] != 1 {
			t.Fatalf("candidate %q walked rungs %v, want [0 1]", candidate, rungs)
		}
	}
	// The report is plan-ordered even though completion order is not.
	for index := 1; index < len(report.Attempts); index++ {
		if report.Attempts[index-1].Rank > report.Attempts[index].Rank {
			t.Fatalf("report is not rank-ordered: %+v", report.Attempts)
		}
	}
	if last := report.Attempts[len(report.Attempts)-1]; last.Rank != 4 || last.Rung != 1 {
		t.Fatalf("last attempt = %+v, want rank 4 rung 1", last)
	}
}

func TestSemanticSearchBatchCacheAvoidsTheSecondProviderRun(t *testing.T) {
	exact := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	identities := []SemanticIdentity{{ID: "person_01", CanonicalSurface: "Alberto Stasi", Kind: "PERSON"}}
	plan := schedulerPlan(t, identities, planCandidate(
		"vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", exact,
	))
	port := &batchTestPort{results: map[string][]SemanticAssetHit{
		exact.Text: {{AssetID: "asset_42", SHA256: strings.Repeat("a", 64)}},
	}}
	clock := &batchTestClock{now: time.Unix(100, 0)}
	cache := &recordingPlanCache{inner: NewSemanticSearchPlanCache(8), entries: make(map[string]SemanticSearchCacheEntry)}
	planner, err := NewSemanticSearchBatchPlanner(port, SemanticSearchBatchConfig{
		MaxConcurrency: 1, Cache: cache, CacheTTL: time.Hour,
		Budget: providerbudget.New(providerbudget.Config{Interval: 0, Burst: 1}),
	})
	if err != nil {
		t.Fatal(err)
	}
	planner.now = clock.Now

	first, err := planner.Run(context.Background(), plan, "it")
	if err != nil {
		t.Fatal(err)
	}
	if first.Metrics.CacheHits != 0 || len(port.calls) != 1 {
		t.Fatalf("cold run = %+v calls=%d, want one provider call", first.Metrics, len(port.calls))
	}
	// The stored entry must carry the answering provider and the lane the query
	// charged: per-provider invalidation is only correct if the cache knows
	// which provider produced the answer.
	if len(cache.entries) != 1 {
		t.Fatalf("cold run stored %d cache entries, want 1", len(cache.entries))
	}
	for _, entry := range cache.entries {
		if entry.Provider != "fake-provider" || entry.ProviderLane != "duckduckgo" {
			t.Fatalf("cached provenance = %q/%q, want fake-provider/duckduckgo", entry.Provider, entry.ProviderLane)
		}
	}

	// The cache is keyed on the normalized entity, not on the query text: a
	// different rung for the same person must still reuse the answer.
	warmQuery := SemanticSearchQuery{Kind: "source_sentence", Text: "Alberto Stasi nel procedimento."}
	coldPlan := schedulerPlan(t, identities, planCandidate(
		"vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", warmQuery,
	))
	clock.Advance(time.Minute)
	warm, err := planner.Run(context.Background(), coldPlan, "it")
	if err != nil {
		t.Fatal(err)
	}
	if warm.Metrics.CacheHits != 1 || warm.Metrics.ProviderQueries != 0 || len(port.calls) != 1 {
		t.Fatalf("warm run = %+v calls=%d, want one cache hit and no provider call", warm.Metrics, len(port.calls))
	}
	if warm.Attempts[0].LatencyMS != 0 || !warm.Attempts[0].Cached {
		t.Fatalf("warm attempt = %+v, want a zero-latency cache hit", warm.Attempts[0])
	}

	expiredPlan := schedulerPlan(t, identities, planCandidate(
		"vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", exact,
	))
	clock.Advance(7 * time.Hour)
	expired, err := planner.Run(context.Background(), expiredPlan, "it")
	if err != nil {
		t.Fatal(err)
	}
	if expired.Metrics.CacheHits != 0 || len(port.calls) != 2 {
		t.Fatalf("expired run = %+v calls=%d, want a fresh provider call", expired.Metrics, len(port.calls))
	}
}

func TestSemanticSearchBatchRetries429OnTheSameRung(t *testing.T) {
	exact := SemanticSearchQuery{Kind: "exact_mention", Text: "Corte d'Appello"}
	identities := []SemanticIdentity{{ID: "org_01", CanonicalSurface: "Corte d'Appello", Kind: "ORG"}}
	plan := schedulerPlan(t, identities, planCandidate(
		"vc_0001", "entity_search", "s01", 1, []string{"org_01"}, "entity:org_01", exact,
	))
	port := newSchedulerTestPort()
	port.limited[exact.Text] = 1
	port.hits[exact.Text] = []SemanticAssetHit{{AssetID: "asset_429", SHA256: strings.Repeat("e", 64)}}
	planner, err := NewSemanticSearchBatchPlanner(port, SemanticSearchBatchConfig{
		MaxConcurrency: 1, RateLimitRetries: 1,
		Budget: providerbudget.New(providerbudget.Config{Interval: 0, Burst: 1, Cooldown: 10 * time.Millisecond}),
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := planner.Run(context.Background(), plan, "it")
	if err != nil {
		t.Fatalf("batch failed: %v", err)
	}
	if report.Metrics.Hits != 1 || !report.Attempts[0].RateLimited {
		t.Fatalf("429 retry report = %+v", report.Attempts[0])
	}
	if got := port.calls["vc_0001"]; len(got) != 2 || got[0] != 0 || got[1] != 0 {
		t.Fatalf("429 retry walked %v, want the same rung twice", got)
	}
}

type routeSupportPort struct {
	calls int
}

func (p *routeSupportPort) Search(context.Context, SemanticAssetSearchRequest) (SemanticAssetSearchResponse, error) {
	p.calls++
	return SemanticAssetSearchResponse{}, nil
}

func (p *routeSupportPort) SupportsClass(class SemanticSearchClass) bool {
	return class != SearchClassPerson
}

func TestSemanticSearchBatchSkipsClassesThePortCannotAnswer(t *testing.T) {
	exact := SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	identities := []SemanticIdentity{{ID: "person_01", CanonicalSurface: "Alberto Stasi", Kind: "PERSON"}}
	plan := schedulerPlan(t, identities, planCandidate(
		"vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", exact,
	))
	port := &routeSupportPort{}
	planner, err := NewSemanticSearchBatchPlanner(port, SemanticSearchBatchConfig{MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	report, err := planner.Run(context.Background(), plan, "it")
	if err != nil {
		t.Fatalf("batch failed: %v", err)
	}
	if port.calls != 0 {
		t.Fatalf("unsupported class reached the provider %d times", port.calls)
	}
	if report.Metrics.RouteSkips != 1 || report.Metrics.Queries != 0 || len(report.Attempts) != 1 {
		t.Fatalf("route-skip report = %+v", report.Metrics)
	}
	if !report.Attempts[0].RouteSkip || report.Attempts[0].Status != "skipped" {
		t.Fatalf("route-skip attempt = %+v", report.Attempts[0])
	}
	unit := plan.Units[0]
	if unit.SearchStatus != ProductionSearchFailed || unit.Status != ProductionUnitSkipped {
		t.Fatalf("route-skipped unit = %+v", unit)
	}
	if plan.AdmitsIdentity("Alberto Stasi") {
		t.Fatal("a route-skipped visual must lose its image admission")
	}
}

// recordingPlanCache observes every Put so a test can assert what a durable
// implementation would persist, while serving reads from the in-memory cache.
type recordingPlanCache struct {
	inner   SemanticSearchCache
	entries map[string]SemanticSearchCacheEntry
}

func (c *recordingPlanCache) Get(ctx context.Context, key string) (SemanticSearchCacheEntry, bool) {
	return c.inner.Get(ctx, key)
}

func (c *recordingPlanCache) Put(ctx context.Context, key string, entry SemanticSearchCacheEntry) {
	c.entries[key] = entry
	c.inner.Put(ctx, key, entry)
}
