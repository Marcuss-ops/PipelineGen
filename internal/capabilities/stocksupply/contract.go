// Package stocksupply is the StockSupplyResolver capability. It owns
// the local-reuse-first, provider-fallback-later stock resolution logic
// without creating new downloaders, new database tables, or new stock
// -specific databases.
//
// The resolver consumes three typed ports:
//   - LocalSearcher: searches Qdrant (or the catalog backend) for
//     already-indexed assets matching a free-text query.
//   - ProviderRegistry: routes to live providers (artlist, youtube, stock)
//     that implement providers.SearchProvider + providers.FetchProvider.
//   - ClipIngester: ingests a FetchedAsset through the canonical
//     ClipIngestPipeline (download → normalize → hash → store → transcribe
//     → enrich → translate → compose → dispatch).
//
// The resolver itself is provider-agnostic: it receives a list of
// provider names from the caller and iterates through them according to
// the selected Strategy, stopping at the first provider that satisfies
// the target duration.
//
// godlike/07 fail-closed contract: every port is checked at construction;
// nil ports return a typed error. Resolution that exhausts all providers
// without reaching ThresholdMinimumSec returns StateFailed with a
// diagnostic reason.
package stocksupply

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/stocksupply"
)

// ── Ports ────────────────────────────────────────────────────────────────

// LocalSearcher queries the local catalog (Qdrant or SQLite) for
// already-indexed assets. Implementations typically wrap the search
// aggregator's catalog backend.
type LocalSearcher interface {
	// SearchCatalog returns asset IDs + metadata for clips matching query
	// in the local index. Results are ranked by relevance.
	SearchCatalog(ctx context.Context, query string, limit int) ([]LocalHit, error)
}

// LocalHit is one locally-found asset that matches a stock query.
type LocalHit struct {
	AssetID        string
	Source         string
	SourceRef      string
	DurationMs     int64
	Title          string
	FileHash       string
	DriveFileID    string
	SourceURL      string
	YouTubeID      string
	RelevanceScore float64
}

// ProviderRegistry is the resolver-side view of the provider registry.
// The implementation delegates to providers.Registry.
type ProviderRegistry interface {
	// SearchProvider returns the named SearchProvider, or nil if not found.
	SearchProvider(name string) providers.SearchProvider

	// FetchProvider returns the named FetchProvider, or nil if not found.
	FetchProvider(name string) providers.FetchProvider
}

// ClipIngester ingests a fetched asset through the canonical
// ClipIngestPipeline and AssetMutationDispatcher. Single-method contract
// so the resolver treats the pipeline as a black box.
type ClipIngester interface {
	// IngestFromFetch ingests a FetchedAsset into media_assets, Drive,
	// and Qdrant via the canonical pipeline. Returns the asset ID and
	// the indexed duration in milliseconds.
	IngestFromFetch(ctx context.Context, sourceRef providers.FetchRequest) (assetID string, durationMs int64, err error)
}

// ── Progress observer ────────────────────────────────────────────────────

// ProgressObserver receives state-transition notifications from the
// resolver. It is optional: NewResolver wires a nil observer;
// NewResolverWithObserver accepts a concrete one.
//
// The observer MUST be goroutine-safe and non-blocking: Prefetch invokes
// it from a background goroutine while the caller may already be running
// scene resolution. A slow observer stalls the prefetch pipeline.
type ProgressObserver interface {
	OnProgress(ev ProgressEvent)
}

// ProgressEvent is one state-transition notification. It carries a
// self-consistent snapshot of the resolution at the moment of the
// transition, so observers can render progressive readiness without
// holding any resolver-internal lock.
type ProgressEvent struct {
	// Query is the semantic query this event refers to.
	Query string
	// State is the new state of that query.
	State stocksupply.SupplyState
	// DurationSec is the usable footage acquired for this query so far.
	DurationSec int
	// TotalSec is the aggregate usable footage across all queries so far.
	TotalSec int
	// TargetSec / MinimumSec mirror the normalised resolution target.
	TargetSec  int
	MinimumSec int
	// ProviderUsed is the last provider that supplied footage ("" = none).
	ProviderUsed string
	// FallbackReason is a human-readable reason when a provider was
	// skipped or fell through (e.g. zero candidates, missing fetch port).
	FallbackReason string
	// NewAssets / ReusedAssets are per-query counters at this transition.
	NewAssets    int
	ReusedAssets int
	// Error is non-empty when this transition is an error/failure.
	Error string
	// At is the wall-clock time of the transition.
	At time.Time
}

// ── Resolver interface ───────────────────────────────────────────────────

// StockSupplyResolver is the canonical, single-owner surface for
// local-reuse-first, provider-fallback-later stock resolution.
//
// godlike/06 SSOT: NO other package may duplicate provider-ordering
// logic, target-duration semantics, or readiness tracking. All
// stock-provisioning code paths MUST route through this interface.
type StockSupplyResolver interface {
	// Resolve runs the full resolution loop synchronously:
	//
	//   1. LOCAL: search Qdrant/catalog for each query
	//   2. ASSESS: sum local durations; if ≥ target, stop (warm reuse)
	//   3. PROVIDER: walk the ordered provider list per Strategy;
	//      for each query that is still under-supplied, search live,
	//      fetch the best candidates, ingest via ClipIngester.
	//   4. RETURN: SupplyResult with per-query breakdown + aggregate stats.
	//
	// ctx controls cancellation; the resolver propagates it to every
	// downstream call (search, fetch, ingest).
	//
	// Returns a typed SupplyResult even on partial success; the error
	// is non-nil only when every query failed and no usable footage
	// was acquired.
	Resolve(ctx context.Context, q stocksupply.SupplyQuery) (*stocksupply.SupplyResult, error)

	// Prefetch runs the resolution in a background goroutine — designed
	// to be launched concurrently with script generation so most of the
	// stock-acquisition latency is hidden behind LLM/NLP/TTS time.
	//
	// Progressive readiness: Prefetch returns as soon as MinimumReadySec
	// is satisfied (StatePartialReady snapshot) WITHOUT waiting for the
	// full TargetDurationSec. The background goroutine keeps fetching to
	// reach the full target; every remaining transition is delivered to
	// the wired ProgressObserver. If the full result is already available
	// when the readiness signal fires, the full result is returned.
	Prefetch(ctx context.Context, q stocksupply.SupplyQuery) (*stocksupply.SupplyResult, error)
}

// ── Value types used by the resolver (not part of the kernel contract) ──

// queryProgress tracks a single query's resolution progress.
type queryProgress struct {
	Query           string
	State           stocksupply.SupplyState
	LocalHits       []LocalHit
	LocalSumMs      int64
	LocalCandidates int
	FetchedIDs      []string
	FetchedSumMs    int64
	Provider        string
	FallbackReason  string
	Error           string
	StartedAt       time.Time
	LocalAt         time.Time
	ProviderAt      time.Time
	FetchedAt       time.Time
	IngestedAt      time.Time
	DoneAt          time.Time
	LocalMs         int64
	ProviderMs      int64
	FetchMs         int64
	IngestMs        int64
}

// thresholdEval evaluates whether the resolver has enough footage.
type thresholdEval struct {
	Satisfied    bool
	CurrentSec   int
	TargetSec    int
	MinimumSec   int
	CanEarlyExit bool
	Gap          int // seconds still needed
}
