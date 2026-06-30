package providers

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ── Type surface ────────────────────────────────────────────────────────

// SearchQuery is the canonical inbound query for the aggregator.
// Decoupled from providers.SearchRequest because the aggregator is the
// LATERAL fan-out orchestration surface (it does not own pagination
// off-by-one with a single provider); the field subset here is the
// minimum the aggregator transforms into per-provider SearchRequest.
//
// Mirrors the user-facing query knobs rather than the per-provider
// filter language (SearchFilters) — the aggregator handles the
// cross-provider translation when it builds the per-provider
// SearchRequest below.
type SearchQuery struct {
	// Query is the free-form term matched against each provider.
	Query string
	// MediaType is the cross-provider type filter ("", "video",
	// "image", "audio", "all"). Empty means "any".
	MediaType string
	// Topic is a soft hint forwarded as SearchRequest.TopicOnly
	// when non-empty.
	Topic string
}

// AggregateOptions drives one Aggregate call. Cursor + Limit
// implement opaque pagination: decode Cursor → Offset applied under
// each provider's NextPageToken semantics; Limit caps the cross-
// provider hit count returned.
//
// S3d (June 2026) extended with HashQuery + Sources for the
// deterministic clip-repos dedup path. See Aggregate() for the
// branch point semantics.
type AggregateOptions struct {
	// Cursor is an opaque string produced by a previous Aggregate
	// call. Empty == first page. Decoding failures fall back to
	// first-page semantics (no error) — Cursor is best-effort
	// pagination, not a strict contract.
	Cursor string
	// Limit caps the number of hits in AggregateResult.Hits.
	// 0 == no limit (return ALL hits subject to per-provider
	// limit). Negative == invalid (returned as a typed error).
	Limit int
	// PerProviderTimeout overrides the per-provider call timeout.
	// 0 falls back to ProviderEntry.HealthTimeout() (which itself
	// falls back to DefaultHealthTimeout=5s).
	PerProviderTimeout time.Duration
	// HashQuery (S3d, June 2026) — when non-empty, Aggregate
	// short-circuits the SearchProvider fan-out and dispatches a
	// deterministic DB hash-match lookup against the registered
	// ClipHashSource adapters. Conceptual: "find every clip with
	// this exact MD5 file hash". The fan-out is bounded by the
	// Sources filter (when non-empty) or by ALL registered hash
	// sources. Each hit is synthesised with
	// QdrantScore=RerankScore=FinalScore=1.0 — the hash match is
	// deterministic and we DO NOT apply the 0.65/0.35 blend
	// (that blend is the qdrant+rerank semantic mix used by the
	// semantic path).
	HashQuery string
	// Sources (S3d, June 2026) — restricts the fan-out to a named
	// subset of providers. Empty == ALL registered providers
	// advertise CapabilitySearch (semantic path) OR ALL registered
	// ClipHashSource adapters (HashQuery path). Multiple values
	// are OR-combined; a provider's Name() must equal one of the
	// values verbatim to be included.
	Sources []string
}

// HashHit is the asset-shape projection returned by a
// ClipHashSource on FindClipsByHash. The aggregator defines this
// locally so the providers package does not need to import
// domain/asset; composition-root adapters translate *asset.Asset
// → HashHit at the injection seam. Fields are minimal — the
// handlers consume only what was previously emitted inline from
// `*assets.ClipsRepository.FindClipsByHash`.
type HashHit struct {
	ID           string
	Source       string
	Name         string
	DriveLink    string
	LocalPath    string
	ThumbnailURL string
	MediaType    string
	DurationSec  int
}

// ClipHashSource is the per-source hash-lookup port. S3d
// (June 2026): mirrors the SearchProvider segregation principle
// — a deterministic DB hash-match lookup is a separate concern
// from semantic search and deserves its own typed port rather
// than overloading SearchProvider.Search. Implementations return
// the canonical HashHit slice (no *asset.Asset in this layer).
//
// Nil-tolerant FindClipsByHash is permitted (returns
// (nil, nil) = "no candidates"). Aggregator.Aggregate handles
// the len()=0 case identically to "found nothing" — the
// distinction is collapsed into the empty-hits path.
type ClipHashSource interface {
	// Name is the canonical provider identity that the
	// CompositionRoot registers the adapter under. Must match
	// the key used in SearchAggregator.SetHashSources.
	Name() string
	// FindClipsByHash returns every clip-row in the source
	// whose file_hash exactly matches the given hash. An empty
	// result is a normal case (no duplicates). Idempotent.
	FindClipsByHash(ctx context.Context, fileHash string) ([]HashHit, error)
}

// ScoredHit is the cross-provider ranked item in an AggregateResult.
// Wraps providers.Candidate plus the two score components that
// contribute to the final blend (qdrantScore vs rerankScore).
//
// S3b (June 2026): the user spec's blend
// `final = qdrantScore * 0.65 + rerankScore * 0.35` is reproduced
// by `reranker.MixedScore(qdrant, rerank, 0.35)` — see
// internal/infrastructure/ai/reranker/scoring.go.
//
// S3d (June 2026): extended with optional asset-shape fields
// (SourceID, SourceSource, Name, DriveLink, LocalPath,
// ThumbnailURL) populated ONLY by the HashQuery path. The
// semantic-search path leaves these at zero values. Handlers
// route on the call-site context: FindDuplicates reads the
// direct fields; SearchAdvanced reads Candidate.
type ScoredHit struct {
	Candidate    Candidate
	ProviderName string
	// QdrantScore is the hit's bi-encoder similarity (cosine 0-1).
	// Today: propagated from Candidate.Score by the aggregator.
	QdrantScore float64
	// RerankScore is the cross-encoder reranker output (0-1).
	// Today: synthesised in the aggregator because no real
	// CrossEncoder is wired (see godlike/07 EXPAND migration note on
	// Scoring). Future S3c-S3d waves will replace the synthesis with
	// the canonical reranker.Client.Score call.
	RerankScore float64
	// FinalScore is the blended score applied across providers.
	FinalScore float64
	// S3d (June 2026): the HashQuery path. SourceID is the
	// clip-row id; SourceSource is the source string (e.g.
	// "artlist" / "youtube" / "stock"); Name is the clip name;
	// DriveLink / LocalPath / ThumbnailURL are the asset-shape
	// projections emitted by FindClipsByHash. Zero on the
	// semantic-search path.
	SourceID     string
	SourceSource string
	Name         string
	DriveLink    string
	LocalPath    string
	ThumbnailURL string
}

// AggregateResult is the typed reply of Aggregate. Partial-results
// contract: ProviderErrors carries per-provider error; Hits is the
// provider-mixed sorted list; Total is the count across all
// providers (NOT the Limit-bounded Hits slice — useful for paging UI
// that wants "X total matches, showing Y"). Cursor is the
// next-page token (empty == last page).
type AggregateResult struct {
	Hits           []ScoredHit
	ProviderErrors map[string]error
	Total          int
	Cursor         string
}

// ── SearchAggregator ───────────────────────────────────────────────────

// defaultAggregateWorkers caps the parallel fan-out slot count.
// S3b spec wording: "fan-out parallelo bounded via
// pkg/concurrent.WithContext(parent)". The user named WithContext for
// the goroutine scoping convention (parent cancellation propagation)
// but we use a CUSTOM bounded semaphore here because
// pkg/concurrent.Group is first-error-wins — incompatible with
// partial-results semantics. The semaphore size is 4 by default;
// adjust via SetMaxConcurrency on the aggregator (future surface).
const defaultAggregateWorkers = 4

// SearchAggregator orchestrates the cross-provider fan-out backed by
// the canonical *Registry. Holds:
//   - the registry (read-only after construction; never mutates it);
//   - a per-provider stats mutex protecting ProviderStats updates
//     under concurrent fan-out.
//   - the ClipHashSource adapters (S3d, June 2026) keyed by
//     provider Name(); registered via SetHashSources before any
//     Aggregate call that uses HashQuery != "".
//
// The aggregator is deliberately stateless beyond the stats counters;
// Aggregate calls are independent and idempotent w.r.t. the registry
// state at the moment of invocation.
type SearchAggregator struct {
	registry *Registry

	statsMu sync.RWMutex
	stats   map[string]*ProviderStats

	maxWorkers int

	// hashSources (S3d, June 2026): keyed clip-store adapters that
	// answer FindClipsByHash queries for the HashQuery branch of
	// Aggregate. Nil/empty when the composition root has not
	// registered any — the HashQuery path then returns an empty
	// result rather than failing. Read-only after SetHashSources
	// is called; concurrent reads use statsMu.RLock() which
	// reuses the existing path lock without adding a second mutex.
	hashSources map[string]ClipHashSource
}

// NewSearchAggregator constructs an aggregator against the given
// registry. The registry must already be populated (typical call
// site: composition root post-WireRegistry.Freeze). The aggregator
// does NOT freeze the registry itself — that's the composition
// root's responsibility, preserving AGENTS.md zero-legacy policy
// on registry ownership.
//
// hashSources is initialised empty; composition root populates it
// via SetHashSources BEFORE the first Aggregate call with
// opts.HashQuery != "". The semantic-search path works without
// hashSources being populated.
func NewSearchAggregator(reg *Registry) *SearchAggregator {
	return &SearchAggregator{
		registry:    reg,
		stats:       make(map[string]*ProviderStats),
		hashSources: make(map[string]ClipHashSource),
		maxWorkers:  defaultAggregateWorkers,
	}
}

// SetMaxConcurrency overrides the default aggregation worker cap.
// Useful for tests that want to force a serial fan-out (set to 1).
// Must be > 0; 0 falls back to defaultAggregateWorkers.
func (a *SearchAggregator) SetMaxConcurrency(n int) {
	if n <= 0 {
		n = defaultAggregateWorkers
	}
	a.statsMu.Lock()
	a.maxWorkers = n
	a.statsMu.Unlock()
}

// SetHashSources registers the per-source ClipHashSource adapters.
// S3d (June 2026): the composition root wires this up by mapping
// every clip-store repo (e.g. *assets.ClipsRepository for
// artlist/youtube/stock) through a thin adapter that satisfies
// ClipHashSource. Call this BEFORE the first Aggregate call with
// opts.HashQuery != "".
//
// A nil sources argument is normalised to an empty map. The map
// replaces (not merges into) the previous registration, so the
// caller owns full lifecycle of the source list. Concurrent
// reads from Aggregate take statsMu.RLock so the swap is atomic
// with respect to in-flight calls.
func (a *SearchAggregator) SetHashSources(sources map[string]ClipHashSource) {
	if a == nil {
		return
	}
	if sources == nil {
		sources = map[string]ClipHashSource{}
	}
	a.statsMu.Lock()
	a.hashSources = sources
	a.statsMu.Unlock()
}

// ── Compile-time+runtime helpers ───────────────────────────────────────

// Sentinel for callers that want to errors.Is on aggregator-level
// errors. Distinct from per-provider errors returned via
// ProviderErrors — the latter is provider-specific, this one is
// aggregator-specific.
var ErrAggregatorNotWired = errors.New("providers: SearchAggregator is not wired (registry nil)")

// SeedUnused keeps the imports stable if the file shrinks below the
// threshold that uses fmt.Errorf above. Without this the `fmt` and
// `errors` imports would compile-fail on a future size shrink.
// Cheap to keep and self-cancelling once Go's compiler flags
// unused imports (this line is a value-discarding assignment that
// Go does not flag).
var _ = ErrAggregatorNotWired
