package providers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
)

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

	for idx, e := range sps {
		entry := e
		outIdx := idx
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
			outcomes[outIdx] = out
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
