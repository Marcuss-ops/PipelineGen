// Package stocksupply — resolver.go is the canonical StockSupplyResolver
// implementation.
//
// godlike/06 SSOT: this struct is the SINGLE owner of local-reuse-first,
// provider-fallback-later stock resolution. The 3 ports (LocalSearcher,
// ProviderRegistry, ClipIngester) are checked at construction with a
// typed fail-closed error per godlike/07.
//
// Progressive readiness: Prefetch launches resolution in a background
// goroutine and returns a StatePartialReady snapshot as soon as
// MinimumReadySec is satisfied, so scene resolution can begin without
// waiting for the full target. The remaining work continues in the
// background; every transition is delivered to the wired ProgressObserver.
//
// Full state machine per query:
//
//	PENDING → SEARCHING_LOCAL → SEARCHING_PROVIDER → FETCHING →
//	INGESTING → INDEXING → PARTIAL_READY → READY (or FAILED)
package stocksupply

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/stocksupply"
)

// ── Construction ──────────────────────────────────────────────────────────

// ErrStockSupplyClosed is returned by NewResolver / NewResolverWithObserver
// when any required port is nil. Message includes the failing port name.
type ErrStockSupplyClosed struct{ Port string }

func (e ErrStockSupplyClosed) Error() string {
	return fmt.Sprintf("stocksupply: nil port — %s (godlike/07 fail-closed)", e.Port)
}

type resolver struct {
	local    LocalSearcher
	reg      ProviderRegistry
	ingest   ClipIngester
	observer ProgressObserver
}

func NewResolver(local LocalSearcher, reg ProviderRegistry, ingest ClipIngester) (StockSupplyResolver, error) {
	return NewResolverWithObserver(local, reg, ingest, nil)
}

func NewResolverWithObserver(local LocalSearcher, reg ProviderRegistry, ingest ClipIngester, observer ProgressObserver) (StockSupplyResolver, error) {
	if local == nil {
		return nil, ErrStockSupplyClosed{Port: "LocalSearcher"}
	}
	if reg == nil {
		return nil, ErrStockSupplyClosed{Port: "ProviderRegistry"}
	}
	if ingest == nil {
		return nil, ErrStockSupplyClosed{Port: "ClipIngester"}
	}
	return &resolver{local: local, reg: reg, ingest: ingest, observer: observer}, nil
}

// ── Resolve (synchronous, full-target) ────────────────────────────────────

func (r *resolver) Resolve(ctx context.Context, q stocksupply.SupplyQuery) (*stocksupply.SupplyResult, error) {
	return r.resolveCore(ctx, q, r.observer)
}

// ── Prefetch (background, progressive readiness) ─────────────────────────

func (r *resolver) Prefetch(ctx context.Context, q stocksupply.SupplyQuery) (*stocksupply.SupplyResult, error) {
	if err := r.validateQuery(q); err != nil {
		return nil, err
	}
	target := r.normaliseTarget(q.Target)

	// Wrap the wired observer with a coordinator that signals minimum
	// readiness. Even without a user observer, the coordinator tracks
	// per-query state so Prefetch can return a snapshot on early exit.
	coord := newPrefetchCoordinator(r.observer, target)
	done := make(chan prefetchOutcome, 1)

	go func() {
		result, err := r.resolveCore(ctx, q, coord)
		done <- prefetchOutcome{result: result, err: err}
	}()

	// Prefer a finished full result over a partial snapshot.
	select {
	case out := <-done:
		return out.result, out.err
	default:
	}

	select {
	case out := <-done:
		return out.result, out.err
	case <-coord.ready:
		return coord.snapshot(), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ── Core resolution (used by both Resolve and Prefetch) ───────────────────

func (r *resolver) resolveCore(ctx context.Context, q stocksupply.SupplyQuery, obs ProgressObserver) (*stocksupply.SupplyResult, error) {
	target := r.normaliseTarget(q.Target)
	limit := q.SearchLimit
	if limit <= 0 {
		limit = 10
	}
	shareSec := target.TargetDurationSec / max(1, len(q.Queries))

	progress := r.initialiseProgress(q.Queries)

	// Emit PENDING for every query.
	for _, p := range progress {
		r.emit(obs, p, stocksupply.StatePending, progress, target)
	}

	// ── Phase 1: Local search ────────────────────────────────────────
	if q.ReuseExisting {
		r.phaseLocalSearch(ctx, progress, limit, obs, target)
	}
	if eval := r.evaluateThreshold(progress, target); eval.Satisfied {
		r.finalise(progress, target, eval, obs)
		return r.buildResult(progress, stocksupply.StateReady), nil
	}

	// ── Phase 2: Provider fallback ───────────────────────────────────
	for _, name := range r.resolveProviderOrder(q.Providers, q.Strategy) {
		if r.evaluateThreshold(progress, target).Satisfied {
			break
		}
		r.phaseProviderSearch(ctx, progress, name, limit, target, shareSec, obs)
	}

	// ── Phase 3: Finalise ────────────────────────────────────────────
	eval := r.evaluateThreshold(progress, target)
	r.finalise(progress, target, eval, obs)

	switch {
	case eval.Satisfied:
		return r.buildResult(progress, stocksupply.StateReady), nil
	case eval.CanEarlyExit:
		return r.buildResult(progress, stocksupply.StatePartialReady), nil
	case eval.CurrentSec == 0:
		return r.buildResult(progress, stocksupply.StateFailed),
			fmt.Errorf("stocksupply: all queries failed across %d provider(s)", len(r.resolveProviderOrder(q.Providers, q.Strategy)))
	default:
		return r.buildResult(progress, stocksupply.StatePartialReady), nil
	}
}

// ── Phase implementations ────────────────────────────────────────────────

func (r *resolver) phaseLocalSearch(ctx context.Context, progress []*queryProgress, limit int, obs ProgressObserver, target stocksupply.SupplyTarget) {
	var wg sync.WaitGroup
	for _, prog := range progress {
		wg.Add(1)
		go func(p *queryProgress) {
			defer wg.Done()
			p.State = stocksupply.StateSearchingLocal
			r.emit(obs, p, p.State, progress, target)
			p.LocalAt = time.Now()
			hits, err := r.local.SearchCatalog(ctx, p.Query, limit)
			p.LocalMs = time.Since(p.LocalAt).Milliseconds()
			if err != nil {
				p.Error = fmt.Sprintf("local search: %v", err)
				return
			}
			p.LocalHits = hits
			for _, h := range hits {
				p.LocalSumMs += h.DurationMs
			}
			p.LocalCandidates = len(hits)
		}(prog)
	}
	wg.Wait()
}

func (r *resolver) phaseProviderSearch(ctx context.Context, progress []*queryProgress, providerName string, searchLimit int, target stocksupply.SupplyTarget, shareSec int, obs ProgressObserver) {
	searchProv := r.reg.SearchProvider(providerName)
	fetchProv := r.reg.FetchProvider(providerName)

	for _, prog := range progress {
		if prog.State == stocksupply.StateReady {
			continue
		}
		if shareSec > 0 && (prog.LocalSumMs+prog.FetchedSumMs) >= int64(shareSec)*1000 {
			continue
		}

		if searchProv == nil {
			prog.FallbackReason = fmt.Sprintf("%s: SearchProvider not found", providerName)
			continue
		}

		prog.State = stocksupply.StateSearchingProvider
		prog.ProviderAt = time.Now()
		r.emit(obs, prog, prog.State, progress, target)

		searchRes, err := searchProv.Search(ctx, providers.SearchRequest{
			Query: prog.Query,
			Limit: searchLimit,
		})
		prog.ProviderMs = time.Since(prog.ProviderAt).Milliseconds()
		if err != nil {
			prog.Error = fmt.Sprintf("provider %q search: %v", providerName, err)
			continue
		}

		candidates := searchRes.Candidates
		if len(candidates) == 0 {
			prog.FallbackReason = fmt.Sprintf("%s: zero candidates for %q", providerName, prog.Query)
			continue
		}

		if fetchProv == nil {
			prog.FallbackReason = fmt.Sprintf("%s: FetchProvider not found", providerName)
			continue
		}

		prog.State = stocksupply.StateFetching
		prog.FetchedAt = time.Now()
		r.emit(obs, prog, prog.State, progress, target)

		for _, cand := range candidates {
			if prog.FetchedSumMs >= int64(target.TargetDurationSec)*1000 {
				break
			}
			if len(prog.FetchedIDs) >= target.MaxClips {
				break
			}

			segEnd := clampDuration(cand.DurationMs, target.ClipDurationMinSec*1000, target.ClipDurationMaxSec*1000)
			if segEnd <= 0 {
				segEnd = cand.DurationMs
			}
			var segStart int64
			if cand.DurationMs > segEnd {
				segStart = (cand.DurationMs - segEnd) / 2
			}

			fetchReq := providers.FetchRequest{
				SourceRef:    cand.SourceRef,
				SegmentStart: time.Duration(segStart) * time.Millisecond,
				SegmentEnd:   time.Duration(segStart+segEnd) * time.Millisecond,
			}

			// INGESTING transition before the pipeline call.
			prog.State = stocksupply.StateIngesting
			r.emit(obs, prog, prog.State, progress, target)

			prog.IngestedAt = time.Now()
			assetID, durationMs, err := r.ingest.IngestFromFetch(ctx, fetchReq)
			prog.IngestMs += time.Since(prog.IngestedAt).Milliseconds()
			if err != nil {
				prog.Error = fmt.Sprintf("ingest from %s: %v", providerName, err)
				continue
			}
			prog.FetchedIDs = append(prog.FetchedIDs, assetID)
			prog.FetchedSumMs += durationMs
			prog.Provider = providerName

			// INDEXING transition after the asset is enqueued for
			// indexing via the canonical outbox / AssetMutationDispatcher
			// path. From the resolver's perspective, the asset is now
			// durable (media_assets row committed + outbox event emitted).
			prog.State = stocksupply.StateIndexing
			r.emit(obs, prog, prog.State, progress, target)
		}
		prog.FetchMs = time.Since(prog.FetchedAt).Milliseconds()
		prog.DoneAt = time.Now()
	}
}

// ── Finalise ──────────────────────────────────────────────────────────────

func (r *resolver) finalise(progress []*queryProgress, target stocksupply.SupplyTarget, eval thresholdEval, obs ProgressObserver) {
	for _, p := range progress {
		totalMs := p.LocalSumMs + p.FetchedSumMs
		switch {
		case totalMs > 0 && eval.Satisfied:
			p.State = stocksupply.StateReady
		case totalMs > 0:
			p.State = stocksupply.StatePartialReady
		default:
			p.State = stocksupply.StateFailed
		}
		r.emit(obs, p, p.State, progress, target)
	}
}

// ── Emit ──────────────────────────────────────────────────────────────────

func (r *resolver) emit(obs ProgressObserver, p *queryProgress, state stocksupply.SupplyState, progress []*queryProgress, target stocksupply.SupplyTarget) {
	if obs == nil {
		return
	}
	obs.OnProgress(ProgressEvent{
		Query:          p.Query,
		State:          state,
		DurationSec:    int((p.LocalSumMs + p.FetchedSumMs) / 1000),
		TotalSec:       sumSec(progress),
		TargetSec:      target.TargetDurationSec,
		MinimumSec:     target.MinimumReadySec,
		ProviderUsed:   p.Provider,
		FallbackReason: p.FallbackReason,
		NewAssets:      len(p.FetchedIDs),
		ReusedAssets:   len(p.LocalHits),
		Error:          p.Error,
		At:             time.Now(),
	})
}

// ── Prefetch coordinator ──────────────────────────────────────────────────

type prefetchOutcome struct {
	result *stocksupply.SupplyResult
	err    error
}

// prefetchCoordinator is a ProgressObserver that:
//   - forwards events to the user observer (if non-nil),
//   - accumulates per-query state so Prefetch can return a snapshot on
//     minimum readiness,
//   - signals a one-shot ready channel when TotalSec ≥ MinimumSec.
type prefetchCoordinator struct {
	inner     ProgressObserver
	mu        sync.Mutex
	byQuery   map[string]*stocksupply.SupplyQueryResult
	order     []string
	totalSec  int
	targetSec int
	minSec    int
	ready     chan struct{}
	readyOnce sync.Once
}

func newPrefetchCoordinator(inner ProgressObserver, target stocksupply.SupplyTarget) *prefetchCoordinator {
	return &prefetchCoordinator{
		inner:     inner,
		byQuery:   make(map[string]*stocksupply.SupplyQueryResult),
		targetSec: target.TargetDurationSec,
		minSec:    target.MinimumReadySec,
		ready:     make(chan struct{}),
	}
}

func (c *prefetchCoordinator) OnProgress(ev ProgressEvent) {
	if c.inner != nil {
		c.inner.OnProgress(ev)
	}
	c.mu.Lock()
	q, ok := c.byQuery[ev.Query]
	if !ok {
		q = &stocksupply.SupplyQueryResult{Query: ev.Query}
		c.byQuery[ev.Query] = q
		c.order = append(c.order, ev.Query)
	}
	q.State = ev.State
	q.DurationSec = ev.DurationSec
	q.ProviderUsed = ev.ProviderUsed
	q.FallbackReason = ev.FallbackReason
	q.LocalCandidates = ev.ReusedAssets
	q.Error = ev.Error
	c.totalSec = ev.TotalSec
	c.mu.Unlock()

	if c.minSec > 0 && ev.TotalSec >= c.minSec {
		c.readyOnce.Do(func() { close(c.ready) })
	}
}

func (c *prefetchCoordinator) snapshot() *stocksupply.SupplyResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	r := &stocksupply.SupplyResult{
		State:            stocksupply.StatePartialReady,
		TotalDurationSec: c.totalSec,
		Queries:          make([]stocksupply.SupplyQueryResult, 0, len(c.order)),
	}
	for _, name := range c.order {
		qr := *c.byQuery[name]
		r.Queries = append(r.Queries, qr)
		if qr.ReuseCount > 0 {
			r.ReusedAssets += qr.ReuseCount
		}
		newCount := qr.AssetCount - qr.ReuseCount
		if newCount > 0 {
			r.NewAssets += newCount
		}
	}
	return r
}

// ── Helpers ──────────────────────────────────────────────────────────────

func (r *resolver) validateQuery(q stocksupply.SupplyQuery) error {
	if len(q.Queries) == 0 {
		return fmt.Errorf("stocksupply: queries list is empty")
	}
	if q.Mode != "" && q.Mode != stocksupply.ModePrefetch &&
		q.Mode != stocksupply.ModeFallback && q.Mode != stocksupply.ModeHybrid {
		return fmt.Errorf("stocksupply: unsupported mode %q", q.Mode)
	}
	if q.Strategy != "" && !q.Strategy.IsValid() {
		return fmt.Errorf("stocksupply: unsupported strategy %q", q.Strategy)
	}
	return nil
}

func (r *resolver) normaliseTarget(t stocksupply.SupplyTarget) stocksupply.SupplyTarget {
	if t.TargetDurationSec <= 0 {
		t.TargetDurationSec = 600
	}
	if t.MinimumReadySec <= 0 {
		t.MinimumReadySec = max(120, t.TargetDurationSec/5)
	}
	if t.MaxClips <= 0 {
		t.MaxClips = 30
	}
	if t.ClipDurationMinSec <= 0 {
		t.ClipDurationMinSec = 4
	}
	if t.ClipDurationMaxSec <= 0 {
		t.ClipDurationMaxSec = 60
	}
	if t.MinimumReadySec > t.TargetDurationSec {
		t.MinimumReadySec = t.TargetDurationSec
	}
	return t
}

func (r *resolver) initialiseProgress(queries []string) []*queryProgress {
	progress := make([]*queryProgress, len(queries))
	for i, q := range queries {
		progress[i] = &queryProgress{
			Query:     q,
			State:     stocksupply.StatePending,
			StartedAt: time.Now(),
		}
	}
	return progress
}

func (r *resolver) resolveProviderOrder(requested []string, strategy stocksupply.ProviderStrategy) []string {
	if len(requested) > 0 {
		return requested
	}
	switch strategy {
	case stocksupply.StrategyArtlistFirst:
		return []string{"artlist", "youtube"}
	case stocksupply.StrategyYouTubeFirst:
		return []string{"youtube", "artlist"}
	case stocksupply.StrategyLocalFirst:
		return nil
	case stocksupply.StrategyParallel:
		return []string{"artlist", "youtube"}
	default:
		return []string{"youtube", "artlist"}
	}
}

func (r *resolver) buildResult(progress []*queryProgress, rootState stocksupply.SupplyState) *stocksupply.SupplyResult {
	result := &stocksupply.SupplyResult{
		State:   rootState,
		Queries: make([]stocksupply.SupplyQueryResult, 0, len(progress)),
	}
	for _, p := range progress {
		totalMs := p.LocalSumMs + p.FetchedSumMs
		result.Queries = append(result.Queries, stocksupply.SupplyQueryResult{
			Query:           p.Query,
			State:           p.State,
			DurationSec:     int(totalMs / 1000),
			AssetCount:      len(p.LocalHits) + len(p.FetchedIDs),
			ReuseCount:      len(p.LocalHits),
			ProviderUsed:    p.Provider,
			FallbackReason:  p.FallbackReason,
			LocalCandidates: len(p.LocalHits),
			SearchMs:        p.ProviderMs,
			DownloadMs:      p.FetchMs,
			IngestMs:        p.IngestMs,
			Error:           p.Error,
		})
		result.TotalDurationSec += int(totalMs / 1000)
		if len(p.FetchedIDs) > 0 {
			result.NewAssets += len(p.FetchedIDs)
		}
		if len(p.LocalHits) > 0 {
			result.ReusedAssets += len(p.LocalHits)
		}
	}
	return result
}

func (r *resolver) evaluateThreshold(progress []*queryProgress, target stocksupply.SupplyTarget) thresholdEval {
	eval := thresholdEval{
		TargetSec:  target.TargetDurationSec,
		MinimumSec: target.MinimumReadySec,
	}
	for _, p := range progress {
		eval.CurrentSec += int((p.LocalSumMs + p.FetchedSumMs) / 1000)
	}
	eval.Satisfied = eval.CurrentSec >= target.TargetDurationSec
	eval.Gap = target.TargetDurationSec - eval.CurrentSec
	if eval.Gap < 0 {
		eval.Gap = 0
	}
	eval.CanEarlyExit = eval.CurrentSec >= target.MinimumReadySec
	return eval
}

func sumSec(progress []*queryProgress) int {
	var total int
	for _, p := range progress {
		total += int((p.LocalSumMs + p.FetchedSumMs) / 1000)
	}
	return total
}

func clampDuration(candidateMs int64, minMs, maxMs int) int64 {
	seg := candidateMs
	if minMs > 0 && seg < int64(minMs) {
		return int64(minMs)
	}
	if maxMs > 0 && seg > int64(maxMs) {
		return int64(maxMs)
	}
	return seg
}