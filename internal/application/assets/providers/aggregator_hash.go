package providers

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

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
