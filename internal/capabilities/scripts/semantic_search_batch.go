package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/providerbudget"
)

// SemanticSearchBatchSchema versions the measured batch report.
const SemanticSearchBatchSchema = "semantic_search_batch.v1"

// SemanticAssetSearchRequest is one exact-source query the planner executes
// against the retrieval provider. The plan owns the query text; the port owns
// network and caching.
type SemanticAssetSearchRequest struct {
	CandidateID  string
	SceneID      string
	Query        string
	QueryKind    string
	QueryOrigin  string
	TemplateID   string
	Illustrative bool
	Rung         int
	Entity       string
	QueryClass   string
	// ProviderLane is the budget lane this query charges. It is derived from
	// QueryClass and is stable enough to appear in an operator report.
	ProviderLane string
	Language     string
	Limit        int
}

// SemanticAssetHit is one certified candidate returned by the provider. The
// location fields are carried because a hit is only consumable downstream when
// it names verified bytes: the overlay binder needs the content address and the
// materializer needs the fetchable location, not just an opaque id.
type SemanticAssetHit struct {
	AssetID string
	URL     string
	SHA256  string
	// DriveLink is a provider locator, not a durable identity. It is carried
	// in-process to the materializer and must never become a serialized second
	// source of truth for the content address.
	DriveLink string `json:"-"`
	MediaType string
	Provider  string
	Entity    string
	Width     int
	Height    int
}

// SemanticAssetSearchResponse is the provider answer for one query.
type SemanticAssetSearchResponse struct {
	Hits     []SemanticAssetHit
	Provider string
}

// SemanticAssetSearchPort is the narrow retrieval port the batch planner needs.
// It is implemented in production over the canonical internet image searcher.
type SemanticAssetSearchPort interface {
	Search(ctx context.Context, req SemanticAssetSearchRequest) (SemanticAssetSearchResponse, error)
}

// SemanticSearchRouteSupport is an optional capability of a port. A port that
// implements it declares which query classes it can actually answer; the
// planner settles the other classes as route skips instead of spending a
// permit on a lane that cannot serve them (a Commons-only probe, for example,
// cannot answer person queries).
type SemanticSearchRouteSupport interface {
	SupportsClass(SemanticSearchClass) bool
}

// SemanticSearchBatchConfig bounds one planned search batch.
type SemanticSearchBatchConfig struct {
	// Limit is the maximum number of assets requested per query.
	Limit int
	// QueryTimeout bounds one provider call, including lane waits and the
	// bounded 429 retries.
	QueryTimeout time.Duration
	// MaxQueries is a safety bound; a plan that needs more aborts instead of
	// looping forever.
	MaxQueries int
	// MaxConcurrency bounds independent ladders that run at once. Ladders for
	// the same candidate are always sequential.
	MaxConcurrency int
	// RateLimitRetries bounds same-rung retries after a 429. Zero means the
	// refusal is reported immediately; the default retries twice.
	RateLimitRetries int
	// Budget is the per-provider token bucket registry. Nil uses the default
	// registry.
	Budget *providerbudget.Registry
	// Cache is the normalized-entity plan cache. Nil uses the default cache
	// unless DisableCache is set.
	Cache SemanticSearchCache
	// CacheTTL is how long a cached provider answer stays valid.
	CacheTTL time.Duration
	// DisableCache turns the plan cache off for a measurement that must hit
	// the provider every time.
	DisableCache bool
}

// DefaultSemanticSearchBatchConfig returns the certified batch bounds: eight
// assets per query, ten seconds per provider call, four independent ladders,
// two same-rung retries after a 429, and a hard safety bound of four attempts
// per planned composition.
func DefaultSemanticSearchBatchConfig() SemanticSearchBatchConfig {
	return SemanticSearchBatchConfig{
		Limit:            8,
		QueryTimeout:     10 * time.Second,
		MaxQueries:       256,
		MaxConcurrency:   4,
		RateLimitRetries: 2,
		Budget:           defaultSemanticSearchBudget(),
		Cache:            NewSemanticSearchPlanCache(512),
		CacheTTL:         time.Hour,
	}
}

// defaultSemanticSearchBudget paces only the lanes that do not already own a
// limiter. Wikimedia Commons is paced by the provider itself (the canonical
// 2s request interval); adding a second wait here would double the production
// cost of every Commons query. A raw probe that bypasses the provider configures
// its own Commons interval in the test.
func defaultSemanticSearchBudget() *providerbudget.Registry {
	registry := providerbudget.New(providerbudget.Config{Interval: 250 * time.Millisecond, Burst: 1, Cooldown: 30 * time.Second})
	registry.Configure("wikimedia_commons", providerbudget.Config{Interval: 0, Burst: 1, Cooldown: 30 * time.Second})
	registry.Configure("duckduckgo", providerbudget.Config{Interval: 300 * time.Millisecond, Burst: 1, Cooldown: 3 * time.Minute})
	registry.Configure("searxng", providerbudget.Config{Interval: 250 * time.Millisecond, Burst: 2, Cooldown: 30 * time.Second})
	return registry
}

func (c SemanticSearchBatchConfig) normalized() SemanticSearchBatchConfig {
	defaults := DefaultSemanticSearchBatchConfig()
	if c.Limit <= 0 {
		c.Limit = defaults.Limit
	}
	if c.QueryTimeout <= 0 {
		c.QueryTimeout = defaults.QueryTimeout
	}
	if c.MaxQueries <= 0 {
		c.MaxQueries = defaults.MaxQueries
	}
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = defaults.MaxConcurrency
	}
	if c.MaxConcurrency > 32 {
		c.MaxConcurrency = 32
	}
	if c.RateLimitRetries < 0 {
		c.RateLimitRetries = 0
	}
	if c.Budget == nil {
		c.Budget = defaults.Budget
	}
	if c.CacheTTL <= 0 {
		c.CacheTTL = defaults.CacheTTL
	}
	if c.DisableCache {
		c.Cache = nil
	} else if c.Cache == nil {
		c.Cache = defaults.Cache
	}
	return c
}

// SemanticSearchAttemptResult is one measured provider attempt.
type SemanticSearchAttemptResult struct {
	CandidateID  string
	SceneID      string
	Query        string
	QueryKind    string
	QueryOrigin  string
	TemplateID   string
	Illustrative bool
	Rung         int
	Rank         int
	Class        SemanticSearchClass
	Entity       string
	// Status is `hit`, `miss`, `error` or `skipped`.
	Status    string
	LatencyMS int64
	Hits      int
	Provider  string
	Error     string
	// RetryAction is the planner decision this attempt produced (`broaden`,
	// `drop_entity`, `skip_visual` or empty on a hit).
	RetryAction string
	AssetIDs    []string
	// Cached reports a plan-cache hit: no provider call was made.
	Cached bool
	// RateLimited reports that the attempt met at least one 429 refusal.
	RateLimited bool
	// RouteSkip reports a composition settled without a provider call because
	// the wired port does not serve its query class.
	RouteSkip bool
}

// SemanticSearchBatchMetrics are measured counts and latencies for one batch.
type SemanticSearchBatchMetrics struct {
	PlannedSearches  int
	Queries          int
	Hits             int
	Misses           int
	Errors           int
	Skipped          int
	Broadens         int
	EntityDrops      int
	CacheHits        int
	ProviderQueries  int
	RateLimitRetries int
	RouteSkips       int
	HitRate          float64
	MeanMS           float64
	P50MS            int64
	P95MS            int64
	TotalMS          int64
}

// SemanticSearchBatchReport is the durable measurement of one executed batch:
// every attempt with its latency and outcome, the aggregates, and the plan's
// feedback after the batch.
type SemanticSearchBatchReport struct {
	Schema       string
	Metrics      SemanticSearchBatchMetrics
	Attempts     []SemanticSearchAttemptResult
	Feedback     *SemanticSearchFeedback
	PlanMetrics  SemanticProductionMetrics
	AbortedError string
}

// SemanticSearchBatchPlanner executes a production plan against a real
// provider. It is the only owner of retrieval outcomes: every attempt goes
// through SemanticProductionPlan.RecordSearchOutcome, so the feedback contract
// and the measurements can never disagree.
//
// Independent compositions run concurrently through a bounded worker pool.
// Ladders for the same candidate stay sequential: the next rung depends on the
// previous outcome, so parallelising inside one ladder would assert a retrieval
// that never happened.
type SemanticSearchBatchPlanner struct {
	port SemanticAssetSearchPort
	cfg  SemanticSearchBatchConfig
	now  func() time.Time

	// planMu serialises plan mutation. Each worker owns one ladder, but all
	// ladders share plan metrics, coverage and feedback.
	planMu sync.Mutex
	// clockMu makes an injected test clock safe when MaxConcurrency > 1.
	clockMu sync.Mutex
	// cacheLocks collapses duplicate same-entity fetches inside one run.
	cacheLocks *concurrent.KeyedLocker
}

// NewSemanticSearchBatchPlanner builds the executor. A nil port is a
// configuration error the caller must not paper over.
func NewSemanticSearchBatchPlanner(port SemanticAssetSearchPort, config ...SemanticSearchBatchConfig) (*SemanticSearchBatchPlanner, error) {
	if port == nil {
		return nil, fmt.Errorf("semantic search batch: a search port is required")
	}
	cfg := DefaultSemanticSearchBatchConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	return &SemanticSearchBatchPlanner{
		port: port, cfg: cfg.normalized(), now: time.Now,
		cacheLocks: concurrent.NewKeyedLocker(),
	}, nil
}

// Run executes every pending media ladder of the plan, records each outcome and
// returns the measured report. A provider error is a miss, never a failed run;
// only cancellation of the parent context or the query budget aborts the batch.
func (p *SemanticSearchBatchPlanner) Run(ctx context.Context, plan *SemanticProductionPlan, language string) (*SemanticSearchBatchReport, error) {
	if p == nil || p.port == nil {
		return nil, fmt.Errorf("semantic search batch: planner is not configured")
	}
	if plan == nil {
		return nil, fmt.Errorf("semantic search batch: plan is required")
	}
	report := &SemanticSearchBatchReport{
		Schema:   SemanticSearchBatchSchema,
		Attempts: make([]SemanticSearchAttemptResult, 0, plan.Metrics.PlannedQueries),
	}
	report.Metrics.PlannedSearches = plan.Metrics.PlannedQueries
	if err := ctx.Err(); err != nil {
		report.AbortedError = err.Error()
		report.Feedback = plan.SearchFeedback()
		report.PlanMetrics = plan.Metrics
		return report, err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	pending := plan.PendingSearchAttempts()
	supported, routeRows := p.routeAttempts(plan, pending)
	report.Attempts = append(report.Attempts, routeRows...)

	started := p.nowTime()
	rows, abortReason := p.dispatch(runCtx, plan, supported, language)
	report.Attempts = append(report.Attempts, rows...)
	sortAttemptRows(report.Attempts)

	p.planMu.Lock()
	report.Feedback = plan.SearchFeedback()
	report.PlanMetrics = plan.Metrics
	p.planMu.Unlock()

	report.Metrics.TotalMS = p.nowTime().Sub(started).Milliseconds()
	if report.Metrics.TotalMS < 0 {
		report.Metrics.TotalMS = 0
	}
	summarizeSemanticSearchBatch(report)

	if err := ctx.Err(); err != nil {
		report.AbortedError = err.Error()
		return report, err
	}
	if abortReason != "" {
		report.AbortedError = abortReason
		return report, fmt.Errorf("semantic search batch: %s", abortReason)
	}
	return report, nil
}

// routeAttempts partitions the pending work by port support. A route skip is
// recorded on the plan with the same skip_visual policy a failed ladder uses,
// so coverage and identity admission cannot claim an image that was never
// searched.
func (p *SemanticSearchBatchPlanner) routeAttempts(plan *SemanticProductionPlan, attempts []SemanticSearchAttempt) ([]SemanticSearchAttempt, []SemanticSearchAttemptResult) {
	support, ok := p.port.(SemanticSearchRouteSupport)
	if !ok {
		return attempts, nil
	}
	supported := make([]SemanticSearchAttempt, 0, len(attempts))
	rows := make([]SemanticSearchAttemptResult, 0)
	for _, attempt := range attempts {
		if support.SupportsClass(attempt.Class) {
			supported = append(supported, attempt)
			continue
		}
		reason := fmt.Sprintf("%s queries are not routed to the wired provider (%s lane)", attempt.Class, attempt.Class.ProviderLane())
		p.planMu.Lock()
		err := plan.SkipUnsupportedUnit(attempt.CandidateID, reason)
		p.planMu.Unlock()
		row := SemanticSearchAttemptResult{
			CandidateID: attempt.CandidateID, SceneID: attempt.SceneID,
			Query: attempt.Query, QueryKind: attempt.QueryKind, QueryOrigin: attempt.QueryOrigin, TemplateID: attempt.TemplateID,
			Illustrative: attempt.Illustrative, Rung: attempt.Rung,
			Rank: attempt.Rank, Class: attempt.Class, Entity: attempt.Entity,
			Status: "skipped", RouteSkip: true,
		}
		if err != nil {
			// The skip itself failed: this is a planner error, not a route
			// decision, so it must stay in the measured error count.
			row.Status = "error"
			row.Error = err.Error()
			row.RouteSkip = false
		} else {
			row.Error = reason
		}
		rows = append(rows, row)
	}
	return supported, rows
}

func (p *SemanticSearchBatchPlanner) nowTime() time.Time {
	p.clockMu.Lock()
	defer p.clockMu.Unlock()
	return p.now()
}

// sortAttemptRows restores plan order after concurrent completion. The report
// is a measurement, so it must not depend on scheduling luck.
func sortAttemptRows(rows []SemanticSearchAttemptResult) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Rank != rows[j].Rank {
			return rows[i].Rank < rows[j].Rank
		}
		if rows[i].CandidateID != rows[j].CandidateID {
			return rows[i].CandidateID < rows[j].CandidateID
		}
		if rows[i].Rung != rows[j].Rung {
			return rows[i].Rung < rows[j].Rung
		}
		return rows[i].Query < rows[j].Query
	})
}

// cacheKey keys the answer on the normalized entity surface, not the raw query
// text: the same person or institution is the same retrieval question even when
// the ladder rung changes between runs. A composition without a single
// canonical identity falls back to its query text.
func cacheKey(attempt SemanticSearchAttempt, language string) string {
	subject := normalizeIdentitySurface(attempt.Entity)
	if subject == "" {
		subject = normalizeIdentitySurface(attempt.Query)
	}
	return strings.Join([]string{subject, string(attempt.Class), strings.ToLower(strings.TrimSpace(language))}, "\x00")
}

// summarizeSemanticSearchBatch derives the aggregate hit rate and latency
// distribution from the measured rows. Percentiles are nearest-rank over real
// provider calls: a cache hit has no provider latency and a route skip has no
// call at all, so neither may move P50/P95.
func summarizeSemanticSearchBatch(report *SemanticSearchBatchReport) {
	if report == nil {
		return
	}
	latencies := make([]int64, 0, len(report.Attempts))
	var total int64
	for _, row := range report.Attempts {
		if row.RouteSkip {
			report.Metrics.RouteSkips++
			continue
		}
		report.Metrics.Queries++
		if row.Cached {
			report.Metrics.CacheHits++
		}
		if row.RateLimited {
			report.Metrics.RateLimitRetries++
		}
		switch row.Status {
		case "hit":
			report.Metrics.Hits++
		case "error":
			report.Metrics.Errors++
			report.Metrics.Misses++
		default:
			report.Metrics.Misses++
		}
		switch row.RetryAction {
		case "broaden":
			report.Metrics.Broadens++
		case "drop_entity":
			report.Metrics.EntityDrops++
		case "skip_visual":
			report.Metrics.Skipped++
		}
		if row.Cached || row.Status == "skipped" {
			continue
		}
		latencies = append(latencies, row.LatencyMS)
		total += row.LatencyMS
	}
	report.Metrics.ProviderQueries = report.Metrics.Queries - report.Metrics.CacheHits
	if report.Metrics.Queries > 0 {
		report.Metrics.HitRate = float64(report.Metrics.Hits) / float64(report.Metrics.Queries)
	}
	if len(latencies) > 0 {
		report.Metrics.MeanMS = float64(total) / float64(len(latencies))
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	report.Metrics.P50MS = nearestRank(latencies, 0.50)
	report.Metrics.P95MS = nearestRank(latencies, 0.95)
}

func nearestRank(sorted []int64, percentile float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(percentile*float64(len(sorted)) + 0.999999)
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
