// Package stocksupply defines the kernel-level contracts for the
// StockSupplyResolver capability. Types declared here are purely
// semantic — they define the shared vocabulary without importing
// application, platform, or infrastructure packages.
//
// The capability-level implementation lives in
// internal/capabilities/stocksupply/ — it wires the Qdrant search port,
// provider registry, and ClipIngestPipeline together without creating
// new downloaders or database tables.
package stocksupply

// ProviderStrategy selects the resolution order when local Qdrant cannot
// satisfy a stock query. The resolver walks the ordered list and stops at
// the first provider that delivers enough candidates above threshold.
type ProviderStrategy string

const (
	// StrategyLocalFirst: Qdrant → if insufficient, do nothing (no live).
	// Used when offline catalogs are sufficient (artlist-heavy projects).
	StrategyLocalFirst ProviderStrategy = "local_first"

	// StrategyArtlistFirst: Qdrant → if insufficient, Artlist → YouTube.
	// Default for generic visual concepts (boxing gym, crowd, nature).
	StrategyArtlistFirst ProviderStrategy = "artlist_first"

	// StrategyYouTubeFirst: Qdrant → if insufficient, YouTube → Artlist.
	// Best for person/event-specific queries (Mike Tyson interview).
	StrategyYouTubeFirst ProviderStrategy = "youtube_first"

	// StrategyFallback is a stable alias for the sequential-fallback path
	// where the resolver tries each registered provider in insertion order,
	// stopping at the first with sufficient supply.
	StrategyFallback ProviderStrategy = "fallback"

	// StrategyParallel fans out to all providers concurrently, then ranks
	// the combined results on arrival. Experimental — certify sequential
	// fallback first before enabling in production.
	StrategyParallel ProviderStrategy = "parallel"
)

// IsValid reports whether s is a recognised strategy value.
func (s ProviderStrategy) IsValid() bool {
	switch s {
	case StrategyLocalFirst, StrategyArtlistFirst, StrategyYouTubeFirst,
		StrategyFallback, StrategyParallel:
		return true
	default:
		return false
	}
}

// SupplyMode selects when stock resolution happens relative to script
// generation or scene rendering.
type SupplyMode string

const (
	// ModeOff: no automatic stock resolution (manual only).
	ModeOff SupplyMode = "off"

	// ModePrefetch: resolve stock concurrently with script generation.
	// Hides stock-acquisition latency behind the time already spent on
	// LLM/NLP/TTS. Progressive readiness: minimum_ready_supply unblocks
	// scene resolution early while remaining stock continues to arrive.
	ModePrefetch SupplyMode = "prefetch"

	// ModeFallback: resolve stock on-demand during scene resolution.
	// Each scene that cannot find a local candidate triggers a live
	// provider search → fetch → ingest → index → assign cycle.
	ModeFallback SupplyMode = "fallback"

	// ModeHybrid: prefetch at start AND live fallback for anything
	// that is still missing during scene resolution.
	ModeHybrid SupplyMode = "hybrid"
)

// SupplyState tracks the lifecycle of one stock query.
type SupplyState string

const (
	StatePending           SupplyState = "PENDING"
	StateSearchingLocal    SupplyState = "SEARCHING_LOCAL"
	StateSearchingProvider SupplyState = "SEARCHING_PROVIDER"
	StateFetching          SupplyState = "FETCHING"
	StateIngesting         SupplyState = "INGESTING"
	StateIndexing          SupplyState = "INDEXING"
	StatePartialReady      SupplyState = "PARTIAL_READY"
	StateReady             SupplyState = "READY"
	StateFailed            SupplyState = "FAILED"
)

// IsTerminal reports whether s is a final state (READY or FAILED).
func (s SupplyState) IsTerminal() bool {
	return s == StateReady || s == StateFailed
}

// SupplyTarget specifies how much usable footage the resolver should aim for.
type SupplyTarget struct {
	// TargetDurationSec is the desired total usable duration in seconds.
	// 0 = provider default.
	TargetDurationSec int

	// MinimumReadySec is the progressive-readiness threshold. Once the
	// resolver has at least this many seconds indexed, downstream consumers
	// (scene resolver, script generator) can begin work without waiting
	// for the full target duration. Must be ≤ TargetDurationSec.
	MinimumReadySec int

	// MaxClips caps the number of segments (4–60 s each) sourced per query.
	// 0 = provider default (suggested: 30).
	MaxClips int

	// ClipDurationMinSec / ClipDurationMaxSec constrain the usable
	// segment range. Both 0 = provider default (4–60 s).
	ClipDurationMinSec int
	ClipDurationMaxSec int
}

// SupplyQuery is the shared input shape for a resolution request.
type SupplyQuery struct {
	// Queries is the list of semantic queries to resolve
	// (e.g. ["Mike Tyson interview", "boxing gym training"]).
	Queries []string

	// Target describes how much footage the resolver should aim for.
	Target SupplyTarget

	// Strategy picks the provider ordering when local is insufficient.
	Strategy ProviderStrategy

	// Mode selects when resolution happens.
	Mode SupplyMode

	// Providers restricts resolution to these named providers (empty = all).
	// Recognised names: "local", "artlist", "youtube".
	Providers []string

	// ReuseExisting, when true, prefers local Qdrant hits over live search.
	// Default true. Set to false to force a fresh provider search.
	ReuseExisting bool

	// SearchLimit caps results returned per live provider search. 0 = 10.
	SearchLimit int
}

// SupplyResult is the output of a completed (or partially-completed) resolution.
type SupplyResult struct {
	// State is the terminal state (READY / FAILED / PARTIAL_READY).
	State SupplyState `json:"state"`

	// TotalDurationSec is the sum of all usable clip durations acquired.
	TotalDurationSec int `json:"total_duration_sec"`

	// NewAssets is the count of freshly ingested assets (0 = pure warm reuse).
	NewAssets int `json:"new_assets"`

	// ReusedAssets is the count of locally cached assets that satisfied queries.
	ReusedAssets int `json:"reused_assets"`

	// Queries is a per-query breakdown for observability.
	Queries []SupplyQueryResult `json:"queries"`
}

// SupplyQueryResult carries the per-query outcome.
type SupplyQueryResult struct {
	Query           string      `json:"query"`
	State           SupplyState `json:"state"`
	DurationSec     int         `json:"duration_sec"`
	AssetCount      int         `json:"asset_count"`
	ReuseCount      int         `json:"reuse_count"`
	ProviderUsed    string      `json:"provider_used,omitempty"`
	FallbackReason  string      `json:"fallback_reason,omitempty"`
	LocalCandidates int         `json:"local_candidates"`
	SearchMs        int64       `json:"search_ms"`
	DownloadMs      int64       `json:"download_ms"`
	IngestMs        int64       `json:"ingest_ms"`
	Error           string      `json:"error,omitempty"`
}
