package contract

import (
	generationcontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/generation"
)

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
	// RecallMS and VerifyMS separate the two stages of LatencyMS. They are
	// zero for cache hits: no provider recall or pixel verification ran.
	RecallMS int64
	VerifyMS int64
	Hits     int
	Provider string
	Error    string
	// RetryAction is the planner decision this attempt produced (`broaden`,
	// `skip_visual` or empty on a hit).
	RetryAction string
	AssetIDs    []string
	// Cached reports a plan-cache hit: no provider call was made.
	Cached bool
	// RateLimited reports that the attempt met at least one 429 refusal.
	RateLimited bool
	// RouteSkip reports a composition settled without a provider call because
	// the wired port does not serve its query class.
	RouteSkip bool
	// ProviderError distinguishes a failed provider call from a provider that
	// answered with no candidates. The two are never the same empty result.
	ProviderError bool
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
	CacheHits        int
	ProviderQueries  int
	RateLimitRetries int
	RouteSkips       int
	HitRate          float64
	MeanMS           float64
	P50MS            int64
	P95MS            int64
	TotalMS          int64
	// RecallMS/VerifyMS are the accumulated stage costs. The P50 variants are
	// nearest-rank over provider attempts, matching P50MS's population.
	RecallMS    int64
	VerifyMS    int64
	RecallP50MS int64
	VerifyP50MS int64
}

// SemanticSearchBatchReport is the durable measurement of one executed batch:
// every attempt with its latency and outcome, the aggregates, and the plan's
// feedback after the batch.
type SemanticSearchBatchReport struct {
	Schema         string
	Metrics        SemanticSearchBatchMetrics
	Attempts       []SemanticSearchAttemptResult
	Feedback       *SemanticSearchFeedback
	PlanMetrics    generationcontract.SemanticProductionMetrics
	AbortedError   string
	ProviderErrors int
	ProviderError  string
}
