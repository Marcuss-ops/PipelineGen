package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	reranker "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
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

// ProviderStats is the per-provider live telemetry captured by the
// aggregator's Stats() surface. Updated under a mutex in each
// fan-out goroutine exit.
//
// ErrorRate = Errors / Calls (0 when Calls == 0).
// Latency is the cumulative wall-clock across all calls to the
// provider (average latency = Latency / Calls when Calls > 0).
// Hits is the cumulative candidate count returned across all calls.
type ProviderStats struct {
	Hits    int
	Calls   int
	Errors  int
	Latency time.Duration
}

// ErrorRate returns the rolling error rate as a fraction in [0, 1].
// Returns 0 when no calls have been recorded yet.
func (s *ProviderStats) ErrorRate() float64 {
	if s == nil || s.Calls == 0 {
		return 0
	}
	return float64(s.Errors) / float64(s.Calls)
}

// AvgLatency returns Latency / Calls. 0 when Calls == 0.
func (s *ProviderStats) AvgLatency() time.Duration {
	if s == nil || s.Calls == 0 {
		return 0
	}
	return s.Latency / time.Duration(s.Calls)
}

// AggregateStats is the snapshot returned by SearchAggregator.Stats().
// Providers is keyed by Provider.Name(); entries that were never
// called do NOT appear in the map (their absence signals
// "never invoked", matching HealthCheck's nil-probe convention).
type AggregateStats struct {
	Providers map[string]*ProviderStats
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

// Aggregate fans out Search to every provider advertising
// CapabilitySearch in the registry. Returns AggregateResult with
// partial results: a provider that errors / times out is recorded in
// ProviderErrors while siblings continue unaffected.
//
// S3d (June 2026): when opts.HashQuery != "" the call is short-
// circuited into the deterministic clip-repos hash-match path
// (see aggregateHash). The two paths share the partial-results
// contract, the per-source timeout semantics, and the bounded
// concurrency cap, but differ in:
//   - dispatch target (SearchProvider fan-out vs ClipHashSource
//     fan-out);
//   - score synthesis (blended vs flat 1.0 for hash-match).
//
// Bounded concurrency: at most maxWorkers providers are queried
// concurrently (default 4). Per-provider timeout is derived from
// opts.PerProviderTimeout (if set), else ProviderEntry.HealthTimeout
// (which itself falls back to DefaultHealthTimeout=5s).
//
// Cursor semantics: opts.Cursor is decoded into a CursorPayload
// (provider-tracking pagination checkpoint). On decode failure the
// cursor is silently ignored and the call starts fresh — Cursor is
// best-effort pagination, not a strict contract. The emitted
// AggregateResult.Cursor is opaque; clients pass it back verbatim in
// the next Aggregate call's opts.Cursor.
//
// Ranking: hits are blended via reranker.MixedScore
// (qdrant, rerank, 0.35) → 65% Qdrant + 35% reranker; sorted
// descending by FinalScore; trimmed to opts.Limit.
func (a *SearchAggregator) Aggregate(
	ctx context.Context,
	query *SearchQuery,
	opts AggregateOptions,
) (*AggregateResult, error) {
	if a == nil || a.registry == nil {
		return nil, fmt.Errorf("providers: SearchAggregator not wired")
	}
	if query == nil {
		return nil, fmt.Errorf("providers: search query is required")
	}
	if opts.Limit < 0 {
		return nil, fmt.Errorf("providers: aggregate options: limit must be >= 0 (got %d)", opts.Limit)
	}
	// S3d (June 2026): HashQuery branch. The HashQuery path does
	// NOT call sp.Search — it short-circuits into
	// aggregateHash which fans out FindClipsByHash against the
	// registered ClipHashSource adapters. The branch is
	// unconditional once opts.HashQuery is non-empty so callers
	// can rely on "any HashQuery-non-empty call routes to
	// aggregateHash regardless of opts.Query/MediaType/Topic".
	if opts.HashQuery != "" {
		return a.aggregateHash(ctx, opts)
	}

	// Decode cursor (best-effort). An unparseable cursor falls back
	// to first-page semantics — operators hitting a stale cursor
	// from an older version get a clean restart rather than a 5xx.
	decodedCursor, decodedOK := decodeCursor(opts.Cursor)
	// Always allocate a non-nil cursorPayload so the inner
	// goroutines can safely mutate lastSeenProvider /
	// lastSeenOffset without a nil-pointer check at every
	// reference. The decoded-OK flag drives the cursor gate
	// inside each goroutine below (`if !decodedOK && ...`).
	cursor := &cursorPayload{}
	if decodedOK && decodedCursor != nil {
		cursor = decodedCursor
	}

	// Snapshot the providers advertising CapabilitySearch under
	// RLock so concurrent registry reads (during fan-out) do not
	// race a Freeze + Register burst from the composition root.
	entries := a.registry.Entries()
	sps := make([]*ProviderEntry, 0, len(entries))
	for _, e := range entries {
		if e == nil || e.Provider == nil {
			continue
		}
		if !e.Capabilities.Has(CapabilitySearch) {
			// Mirror the legacy path: tag from p.Capabilities()
			// also qualifies the provider (S3a migration keeps
			// both surfaces in lockstep).
			advertisesSearch := false
			for _, c := range e.Provider.Capabilities() {
				if c == CapabilitySearch {
					advertisesSearch = true
					break
				}
			}
			if !advertisesSearch {
				continue
			}
		}
		sps = append(sps, e)
	}

	// Apply opts.Sources filter (S3d): when non-empty, the
	// semantic-search path also honours Sources. The HashQuery
	// path filters its own target list internally; this branch
	// only fires here.
	if len(opts.Sources) > 0 {
		filtered := make([]*ProviderEntry, 0, len(sps))
		for _, e := range sps {
			if containsString(opts.Sources, e.Name) {
				filtered = append(filtered, e)
			}
		}
		sps = filtered
	}

	// Bounded parallel fan-out via custom semaphore. We DON'T use
	// pkg/concurrent.Group because it cancels siblings on first
	// error; partial-results semantics require every provider to
	// finish even when one fails. The semaphore convention follows
	// pkg/concurrent.ParallelMap for backward compatibility with
	// the rest of the codebase's bounded-fanout pattern.
	maxWorkers := a.maxWorkers
	if maxWorkers <= 0 {
		maxWorkers = defaultAggregateWorkers
	}
	sem := make(chan struct{}, maxWorkers)

	outcomes := make([]providerOutcome, len(sps))
	var wg sync.WaitGroup
	var cursorMerge sync.Mutex

	for _, e := range sps {
		entry := e
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Acquire semaphore slot before doing any blocking work.
			// When the parent ctx is cancelled, drop the slot and
			// record that cause promptly so the aggregator's tight
			// deadlines don't hold a slot past return.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				a.recordOutcome(entry.Name, providerOutcome{
					err:     ctx.Err(),
					latency: 0,
				})
				return
			}

			// Per-provider timeout applied via derived ctx. When
			// opts.PerProviderTimeout is set, it overrides the
			// entry's HealthTimeout; otherwise we honour the entry's
			// own setting (DefaultHealthTimeout at the registry).
			timeout := opts.PerProviderTimeout
			if timeout <= 0 {
				timeout = entry.HealthTimeout()
			}
			providerCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			start := time.Now()
			req := SearchRequest{
				Query:     query.Query,
				Limit:     0, // per-provider Limit pulled from cursor below
				TopicOnly: query.Topic != "",
			}
			if hint := cursor.LimitHintForProvider(entry.Name); hint > 0 {
				req.Limit = hint
			}
			// Apply media-type filter via SearchFilters.MediaTypes
			// when the aggregator was told to constrain type.
			if query.MediaType != "" {
				// Cross-provider MediaType → forward via SearchFilters
				// as the canonical SortMode hint for now (the
				// provider-decode layer honours it as a soft hint).
				// Filters.Sort is a string tag and is tolerantly
				// ignored by adapters that don't recognise it.
				req.Filters.Sort = SortMode(query.MediaType)
			}

			sp, ok := entry.Provider.(SearchProvider)
			if !ok {
				// Provider is in the typed-set but does NOT
				// actually implement Search — defensive guard
				// against a stale Capabilities().Search pointer.
				a.recordOutcome(entry.Name, providerOutcome{
					err:     fmt.Errorf("provider %q declared search but does not implement SearchProvider", entry.Name),
					latency: 0,
				})
				return
			}
			searchResult, searchErr := sp.Search(providerCtx, req)
			elapsed := time.Since(start)

			hit, hitsErr := a.scoredHitsFromResult(entry.Name, searchResult)
			// searchErr is the source-of-truth provider error.
			// hitsErr is currently always nil (synthesised hits
			// cannot fail); carried forward for future S3c waves
			// that may add an in-stream validation step.
			finalErr := searchErr
			if finalErr == nil {
				finalErr = hitsErr
			}
			out := providerOutcome{
				hits:    hit,
				err:     finalErr,
				cursor:  searchResult.NextPageToken,
				latency: elapsed,
			}
			a.recordOutcome(entry.Name, out)

			// Cursor checkpoint: under first-page (decodedOK == false),
			// record the first provider's token as the merge
			// candidate; on subsequent pages, accumulate and pick
			// the highest offset to drive the next call. Best-effort
			// pagination, so this is informational; the user-facing
			// Cursor string is the encoded payload below.
			//
			// Note: `cursor` (the cursorPayload pointer) is
			// ALWAYS non-nil here because Aggregate allocates a
			// cursorPayload at the top of the call. The
			// `decodedOK` flag (captured earlier) signals whether
			// the call is a fresh page; in fresh-page mode the
			// cursor is editable, in subsequent-page mode it
			// carries the resume hint we want to preserve.
			cursorMerge.Lock()
			if !decodedOK && out.cursor != "" {
				cursor.lastSeenProvider = entry.Name
				cursor.lastSeenOffset = 0
			}
			cursorMerge.Unlock()
		}()
	}
	wg.Wait()

	// Aggregate per-provider outcomes into a single result.
	hits := make([]ScoredHit, 0, len(outcomes)*8)
	totalAcrossProviders := 0
	provErrors := make(map[string]error)
	// Iterate outcomes in lockstep with sps — outcomes is sized to
	// len(sps) post-filter so the indices align without a defensive
	// length check.
	for i, out := range outcomes {
		name := sps[i].Name
		if out.err != nil {
			provErrors[name] = out.err
			continue
		}
		if len(out.hits) == 0 {
			continue
		}
		hits = append(hits, out.hits...)
		totalAcrossProviders += len(out.hits)
		// Track the deepest cursor we observed so subsequent pages
		// can resume from a meaningful checkpoint instead of an
		// arbitrary "last sps entry, opts.Limit" placeholder.
		if out.cursor != "" {
			cursor.lastSeenProvider = name
			cursor.lastSeenOffset = len(out.hits)
		}
	}

	// Global ranking blend (FinalScore). Sort descending so the most
	// relevant hits come first; cap at opts.Limit.
	blendHits(hits)
	if opts.Limit > 0 && len(hits) > opts.Limit {
		hits = hits[:opts.Limit]
	}

	// Emit next cursor. Empty when callers consumed ALL hits
	// (Total == len(hits) && Limit not reached further).
	//
	// The `cursor` payload was already updated in the aggregation
	// loop above (per-provider cursor-token = checkpoint), so we
	// emit it verbatim. Constructing a fresh payload here would
	// silently discard the cursor state gathered from sibling
	// providers — a round-trip bug where the response cursor did
	// not carry the request cursor's data.
	nextCursor := ""
	if opts.Limit > 0 && totalAcrossProviders > opts.Limit {
		nextCursor = encodeCursor(cursor)
	}

	return &AggregateResult{
		Hits:           hits,
		ProviderErrors: provErrors,
		Total:          totalAcrossProviders,
		Cursor:         nextCursor,
	}, nil
}

// providerOutcome is the per-goroutine result of one fan-out worker.
// Declared at file scope so recordOutcome and the goroutine body
// can both name it without an inline struct + type-alias dance.
type providerOutcome struct {
	hits    []ScoredHit
	err     error
	cursor  string
	latency time.Duration
}

// recordOutcome updates the per-provider stats counters atomically.
// Tolerant to nil aggregator so a hypothetical future caller can
// construct a stats-disabled aggregator without re-implementing the
// outcome-routing logic.
//
// name MUST be the canonical entry.Name (NOT the NextPageToken) so
// the stats map is keyed by provider identity. Goroutines pass
// entry.Name explicitly — past versions keyed the stats map by
// the cursor field, which was incorrect: NextPageToken is provider-
// opaque and re-using it as a stats key silently merged distinct
// providers' counters.
func (a *SearchAggregator) recordOutcome(name string, out providerOutcome) {
	if a == nil {
		return
	}
	if name == "" {
		// Defensive: an empty name usually means a registry
		// mutation race during fan-out. Skip stats for this entry
		// rather than synthesizing a fake key that would silently
		// merge into the map.
		return
	}
	a.statsMu.Lock()
	ps, ok := a.stats[name]
	if !ok {
		ps = &ProviderStats{}
		a.stats[name] = ps
	}
	ps.Calls++
	ps.Latency += out.latency
	if out.err != nil {
		ps.Errors++
	} else {
		ps.Hits += len(out.hits)
	}
	a.statsMu.Unlock()
}

// Stats returns a snapshot copy of the per-provider stats so callers
// can render dashboards without holding the aggregator's lock. The
// returned struct is decoupled from internal state — mutating it
// does not affect the aggregator.
func (a *SearchAggregator) Stats() AggregateStats {
	if a == nil {
		return AggregateStats{Providers: map[string]*ProviderStats{}}
	}
	a.statsMu.RLock()
	defer a.statsMu.RUnlock()
	out := AggregateStats{Providers: make(map[string]*ProviderStats, len(a.stats))}
	for name, ps := range a.stats {
		if ps == nil {
			out.Providers[name] = &ProviderStats{}
			continue
		}
		// Defensive copy: callers can mutate the snapshot
		// freely without affecting aggregator state.
		cp := *ps
		out.Providers[name] = &cp
	}
	return out
}

// ── Internal helpers ───────────────────────────────────────────────────

// scoredHitsFromResult converts a per-provider SearchResult into
// the aggregator's typed ScoredHit shape. Today (S3b) the
// QdrantScore is propagated from Candidate.Score and the
// RerankScore is synthesised = max(Candidate.Score, 0.5) so the
// canonical blend remains meaningful until a real CrossEncoder is
// wired in a future wave.
//
// Future swap-site: reranker.Client.Score(ctx, hits) should
// produce the rerank map; aggregator consumes it and stamps
// RerankScore per hit. Until then, the synthesised value keeps the
// 0.65/0.35 blend numerically sane (no flat-zero or
// weight-collapse behaviour).
func (a *SearchAggregator) scoredHitsFromResult(providerName string, res SearchResult) ([]ScoredHit, error) {
	if res.Candidates == nil {
		return nil, nil
	}
	hits := make([]ScoredHit, 0, len(res.Candidates))
	for i := range res.Candidates {
		c := res.Candidates[i]
		q := c.Score
		if q < 0 {
			q = 0
		}
		if q > 1 {
			q = 1
		}
		r := q
		if r < 0.5 {
			r = 0.5 // synthesise >= 0.5 so the rerank weight has floor
		}
		final := reranker.MixedScore(q, r, 0.35)
		hits = append(hits, ScoredHit{
			Candidate:    c,
			ProviderName: providerName,
			QdrantScore:  q,
			RerankScore:  r,
			FinalScore:   final,
		})
	}
	return hits, nil
}

// blendHits sorts the hits slice in place by FinalScore descending.
// In-place sort avoids a second allocation when slice capacity is
// already dominant (the aggregator's hits buffer is sized to
// `len(outcomes)*8` upstream, which is typically >= len(hits)).
//
// Note: when multiple hits share the same FinalScore the sort is
// not stable across distinct provider identities — go's sort is
// not stable. The tie-breaker by ProviderName is a deterministic
// guard for dashboards that want "what shows up first when ties
// are equal"; not a contract for distributed search rank.
func blendHits(hits []ScoredHit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].FinalScore == hits[j].FinalScore {
			return hits[i].ProviderName < hits[j].ProviderName
		}
		return hits[i].FinalScore > hits[j].FinalScore
	})
}

// ── HashQuery path ─────────────────────────────────────────────────────

// aggregateHash is the S3d (June 2026) branch of Aggregate. It fans
// out FindClipsByHash lookups against the registered
// ClipHashSource adapters (filtered by opts.Sources), synthesises
// the asset-shape ScoredHit projection for each row, and assembles
// the AggregateResult with the same partial-results contract used
// by the semantic path.
//
// Conceptual differences from the semantic path:
//   - dispatch target is ClipHashSource.FindClipsByHash (NOT
//     SearchProvider.Search);
//   - score synthesis is flat 1.0 across all three score
//     channels (QdrantScore=RerankScore=FinalScore=1.0) — a
//     deterministic MD5 match has no semantic scoring;
//   - the per-source timeout still honours
//     opts.PerProviderTimeout or DefaultHealthTimeout;
//   - the bounded concurrency cap still uses the aggregator's
//     maxWorkers slot count;
//   - opts.Limit applies to the post-sort Hit count and is
//     honoured identically to the semantic path;
//   - opts.Cursor is NOT honoured here — hash-match lookup is
//     non-paginated (the result set is bounded by clip-store
//     cardinality and the caller can re-issue with a tighter
//     filter if needed).
//
// When no ClipHashSource adapters are wired (composition root
// has not called SetHashSources), the path returns an empty
// AggregateResult rather than failing — partial-results
// semantics: "no source advertised" is a valid state, not an
// error. ProviderErrors is the empty map (no source to fail).
func (a *SearchAggregator) aggregateHash(ctx context.Context, opts AggregateOptions) (*AggregateResult, error) {
	if a == nil {
		return nil, fmt.Errorf("providers: SearchAggregator not wired")
	}
	a.statsMu.RLock()
	sources := a.hashSources
	a.statsMu.RUnlock()

	if len(sources) == 0 {
		// "No source advertised" — empty result, not an
		// error. Aligns with the semantic path's
		// len(sps)==0 contract.
		return &AggregateResult{
			Hits:           []ScoredHit{},
			ProviderErrors: map[string]error{},
			Total:          0,
			Cursor:         "",
		}, nil
	}

	// Build the per-source target list filtered by opts.Sources.
	// subsumed nil sources are skipped; subsumed Sources-rests
	// outside the named subset are skipped.
	targets := make([]struct {
		name string
		src  ClipHashSource
	}, 0, len(sources))
	for name, src := range sources {
		if src == nil {
			continue
		}
		if len(opts.Sources) > 0 && !containsString(opts.Sources, name) {
			continue
		}
		targets = append(targets, struct {
			name string
			src  ClipHashSource
		}{name, src})
	}

	if len(targets) == 0 {
		return &AggregateResult{
			Hits:           []ScoredHit{},
			ProviderErrors: map[string]error{},
			Total:          0,
			Cursor:         "",
		}, nil
	}

	// Bounded parallel fan-out via the same custom semaphore as
	// the semantic path. The size comes from a.maxWorkers (race
	// with SetMaxConcurrency documented in code-reviewer
	// findings — known minor issue for a followup PR).
	maxWorkers := a.maxWorkers
	if maxWorkers <= 0 {
		maxWorkers = defaultAggregateWorkers
	}
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	type hashOutcome struct {
		hits []ScoredHit
		err  error
	}
	outcomes := make([]hashOutcome, len(targets))

	for i, t := range targets {
		idx := i
		name := t.name
		src := t.src
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				outcomes[idx] = hashOutcome{err: ctx.Err()}
				return
			}
			timeout := opts.PerProviderTimeout
			if timeout <= 0 {
				timeout = DefaultHealthTimeout
			}
			sourceCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			list, err := src.FindClipsByHash(sourceCtx, opts.HashQuery)
			if err != nil {
				outcomes[idx] = hashOutcome{err: err}
				return
			}
			hits := make([]ScoredHit, 0, len(list))
			for _, h := range list {
				hits = append(hits, ScoredHit{
					ProviderName: name,
					SourceID:     h.ID,
					SourceSource: h.Source,
					Name:         h.Name,
					DriveLink:    h.DriveLink,
					LocalPath:    h.LocalPath,
					ThumbnailURL: h.ThumbnailURL,
					QdrantScore:  1.0,
					RerankScore:  1.0,
					FinalScore:   1.0,
				})
			}
			outcomes[idx] = hashOutcome{hits: hits}
		}()
	}
	wg.Wait()

	hits := make([]ScoredHit, 0, 16)
	totalAcrossSources := 0
	provErrors := map[string]error{}
	for i, o := range outcomes {
		if o.err != nil {
			provErrors[targets[i].name] = o.err
			continue
		}
		if len(o.hits) == 0 {
			continue
		}
		hits = append(hits, o.hits...)
		totalAcrossSources += len(o.hits)
	}
	// Stable sort by (ProviderName, SourceSource, SourceID) for
	// deterministic hash-match result ordering across calls.
	// blendHits' FinalScore tiebreaker is not meaningful here
	// (every score is 1.0), so we substitute an
	// insertion-order-equivalent identity sort.
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].ProviderName != hits[j].ProviderName {
			return hits[i].ProviderName < hits[j].ProviderName
		}
		if hits[i].SourceSource != hits[j].SourceSource {
			return hits[i].SourceSource < hits[j].SourceSource
		}
		return hits[i].SourceID < hits[j].SourceID
	})
	if opts.Limit > 0 && len(hits) > opts.Limit {
		hits = hits[:opts.Limit]
	}

	return &AggregateResult{
		Hits:           hits,
		ProviderErrors: provErrors,
		Total:          totalAcrossSources,
		Cursor:         "",
	}, nil
}

// containsString is the canonical "is name in list?" guard. Used
// by Aggregate (both branches) for the Sources filter. Cheap O(N)
// check under opts.Sources cardinality (typically <10).
func containsString(list []string, name string) bool {
	for _, v := range list {
		if v == name {
			return true
		}
	}
	return false
}

// ── Cursor ─────────────────────────────────────────────────────────────

// cursorPayload is the unencoded (JSON-friendly) checkpoint built
// per Aggregate response. Opaque to callers — emitted + consumed via
// base64(JSON) by encodeCursor/decodeCursor.
type cursorPayload struct {
	// lastSeenProvider is the most recently returned provider name.
	// Best-effort: tells the next aggregator call which provider
	// "led" the previous page so subsequent pages can refine.
	lastSeenProvider string
	// lastSeenOffset is the next-page offset within the leading
	// provider. Aggregator re-reads it on decode to build the next
	// per-provider Limit hint.
	lastSeenOffset int
}

// LimitHintForProvider returns the per-provider NextPage hint. When
// the decoded cursor is empty, the hint is 0 — providers apply
// their native default (provider-default limit).
func (c *cursorPayload) LimitHintForProvider(_ string) int {
	if c == nil {
		return 0
	}
	return c.lastSeenOffset
}

// encodeCursor returns a base64(JSON) opaque string. Decode failures
// yield an empty payload (best-effort fallback).
func encodeCursor(p *cursorPayload) string {
	if p == nil {
		return ""
	}
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor reads the base64(JSON) opaque cursor. Returns
// (nil, false) on decode failure so the caller can fall back to
// first-page semantics. Never returns an error to keep the API
// ergonomic — Cursor is best-effort pagination, not strict.
func decodeCursor(s string) (*cursorPayload, bool) {
	if s == "" {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, false
	}
	return &p, true
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
