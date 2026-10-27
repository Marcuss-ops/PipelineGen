package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Marcuss-ops/PipelineGen/pkg/providerbudget"
)

// dispatch runs one attempt per pending composition through a bounded worker
// pool. A worker owns its candidate's ladder end to end: after recording an
// outcome it enqueues the next rung of the SAME candidate. Different candidates
// are independent and may run at once.
func (p *SemanticSearchBatchPlanner) dispatch(ctx context.Context, plan *SemanticProductionPlan, initial []SemanticSearchAttempt, language string) ([]SemanticSearchAttemptResult, string) {
	if len(initial) == 0 {
		return nil, ""
	}
	workers := p.cfg.MaxConcurrency
	if workers > len(initial) {
		workers = len(initial)
	}
	jobs := make(chan SemanticSearchAttempt, p.cfg.MaxQueries)
	results := make(chan SemanticSearchAttemptResult, p.cfg.MaxQueries)

	var pending sync.WaitGroup
	var workerWG sync.WaitGroup
	var scheduled atomic.Int64

	var abortMu sync.Mutex
	abortReason := ""
	abort := func(reason string) {
		abortMu.Lock()
		if abortReason == "" {
			abortReason = reason
		}
		abortMu.Unlock()
	}

	enqueue := func(attempt SemanticSearchAttempt) bool {
		if scheduled.Add(1) > int64(p.cfg.MaxQueries) {
			abort(fmt.Sprintf("query budget of %d attempts exhausted", p.cfg.MaxQueries))
			return false
		}
		pending.Add(1)
		select {
		case jobs <- attempt:
			return true
		case <-ctx.Done():
			pending.Done()
			return false
		}
	}

	for i := 0; i < workers; i++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for attempt := range jobs {
				row, next := p.runAttempt(ctx, plan, attempt, language)
				results <- row
				if next != nil {
					enqueue(*next)
				}
				pending.Done()
			}
		}()
	}

	for _, attempt := range initial {
		enqueue(attempt)
	}

	go func() {
		pending.Wait()
		close(jobs)
		workerWG.Wait()
		close(results)
	}()

	rows := make([]SemanticSearchAttemptResult, 0, len(initial))
	for row := range results {
		rows = append(rows, row)
	}
	abortMu.Lock()
	reason := abortReason
	abortMu.Unlock()
	return rows, reason
}

// runAttempt executes exactly one rung, records its outcome on the plan and
// returns the next rung of the same candidate, if the ladder has one.
func (p *SemanticSearchBatchPlanner) runAttempt(ctx context.Context, plan *SemanticProductionPlan, attempt SemanticSearchAttempt, language string) (SemanticSearchAttemptResult, *SemanticSearchAttempt) {
	row := SemanticSearchAttemptResult{
		CandidateID: attempt.CandidateID, SceneID: attempt.SceneID,
		Query: attempt.Query, QueryKind: attempt.QueryKind, QueryOrigin: attempt.QueryOrigin, TemplateID: attempt.TemplateID,
		Illustrative: attempt.Illustrative, Rung: attempt.Rung,
		Rank: attempt.Rank, Class: attempt.Class, Entity: attempt.Entity,
	}
	key := cacheKey(attempt, language)
	response, cached, latency, rateLimited, searchErr := p.fetch(ctx, attempt, language, key)
	row.Cached = cached
	row.LatencyMS = latency
	row.RateLimited = rateLimited

	outcome := SemanticSearchOutcome{CandidateID: attempt.CandidateID, Rung: attempt.Rung}
	switch {
	case searchErr != nil:
		row.Status = "error"
		row.Error = searchErr.Error()
		outcome.Error = row.Error
	case len(response.Hits) == 0:
		row.Status = "miss"
		outcome.Error = "provider returned no candidate"
	default:
		row.Status = "hit"
		row.Hits = len(response.Hits)
		row.Provider = strings.TrimSpace(response.Provider)
		outcome.Found = true
		outcome.AssetID = strings.TrimSpace(response.Hits[0].AssetID)
		for _, hit := range response.Hits {
			if id := strings.TrimSpace(hit.AssetID); id != "" {
				row.AssetIDs = append(row.AssetIDs, id)
			}
		}
		if outcome.AssetID == "" {
			outcome.AssetID = strings.TrimSpace(response.Hits[0].URL)
		}
	}

	p.planMu.Lock()
	next, _, recordErr := plan.RecordSearchOutcome(outcome)
	_ = next
	if recordErr != nil {
		row.Status = "error"
		row.Error = recordErr.Error()
	}
	unit := planUnitByCandidate(plan, attempt.CandidateID)
	if unit != nil {
		if unit.Retry != nil {
			row.RetryAction = unit.Retry.Action
		}
		if row.Status == "hit" && unit.ResolvedImage == nil {
			first := response.Hits[0]
			provider := strings.TrimSpace(first.Provider)
			if provider == "" {
				provider = strings.TrimSpace(response.Provider)
			}
			unit.ResolvedImage = &SemanticResolvedImage{
				AssetID: outcome.AssetID, URL: strings.TrimSpace(first.URL),
				DriveLink: strings.TrimSpace(first.DriveLink), SHA256: strings.TrimSpace(first.SHA256),
				MediaType: strings.TrimSpace(first.MediaType), Provider: provider,
				Query: attempt.Query, QueryOrigin: attempt.QueryOrigin, TemplateID: attempt.TemplateID,
				Illustrative: attempt.Illustrative, Entity: strings.TrimSpace(first.Entity),
				Width: first.Width, Height: first.Height,
			}
		}
	}
	nextAttempt, ok := plan.NextAttemptForCandidate(attempt.CandidateID)
	p.planMu.Unlock()

	if recordErr != nil || !ok {
		return row, nil
	}
	return row, &nextAttempt
}

// fetch serves the answer from the plan cache when possible and otherwise
// performs one budgeted provider call. The keyed lock collapses duplicate
// same-entity fetches inside a single run; different entities stay independent.
func (p *SemanticSearchBatchPlanner) fetch(ctx context.Context, attempt SemanticSearchAttempt, language, key string) (SemanticAssetSearchResponse, bool, int64, bool, error) {
	if p.cfg.Cache == nil {
		response, latency, rateLimited, err := p.searchWithBudget(ctx, attempt, language)
		return response, false, latency, rateLimited, err
	}
	release := p.cacheLocks.Lock(key)
	defer release()
	cacheTTL := p.cfg.CacheTTL
	if attempt.Class == SearchClassLocation || attempt.Class == SearchClassOrganization {
		cacheTTL = p.cfg.StableEntityCacheTTL
	}
	if entry, ok := p.cfg.Cache.Get(ctx, key); ok && p.nowTime().Sub(entry.StoredAt) <= cacheTTL {
		return entry.Response, true, 0, false, nil
	}
	response, latency, rateLimited, err := p.searchWithBudget(ctx, attempt, language)
	if err == nil && len(response.Hits) > 0 {
		storedAt := p.nowTime()
		p.cfg.Cache.Put(ctx, key, SemanticSearchCacheEntry{
			Response: response, StoredAt: storedAt, ExpiresAt: storedAt.Add(cacheTTL),
			Provider: cacheAnswerProvider(response), ProviderLane: attempt.Class.ProviderLane(),
		})
	}
	return response, false, latency, rateLimited, err
}

// cacheAnswerProvider names the provider that actually answered. The adapter
// reports the upstream provider (Commons, DuckDuckGo, SearXNG) on the hit, so
// a per-provider invalidation targets the lane that produced the answer rather
// than the planner that requested it.
func cacheAnswerProvider(response SemanticAssetSearchResponse) string {
	if provider := strings.TrimSpace(response.Provider); provider != "" {
		return provider
	}
	for _, hit := range response.Hits {
		if provider := strings.TrimSpace(hit.Provider); provider != "" {
			return provider
		}
	}
	return ""
}

// searchWithBudget acquires the provider lane, performs the call and retries
// the SAME rung after a 429. The retry is bounded: a persistent refusal becomes
// a miss so the ladder can widen or drop, instead of spending the whole query
// timeout on the same wall.
func (p *SemanticSearchBatchPlanner) searchWithBudget(ctx context.Context, attempt SemanticSearchAttempt, language string) (SemanticAssetSearchResponse, int64, bool, error) {
	queryCtx, cancel := context.WithTimeout(ctx, p.cfg.QueryTimeout)
	defer cancel()
	lane := attempt.Class.ProviderLane()
	request := SemanticAssetSearchRequest{
		CandidateID: attempt.CandidateID, SceneID: attempt.SceneID,
		Query: attempt.Query, QueryKind: attempt.QueryKind, QueryOrigin: attempt.QueryOrigin, TemplateID: attempt.TemplateID,
		Illustrative: attempt.Illustrative, Rung: attempt.Rung,
		Entity: attempt.Entity, QueryClass: string(attempt.Class), ProviderLane: lane,
		Language: strings.TrimSpace(language), Limit: p.cfg.Limit,
	}
	var latencyMS int64
	var rateLimited bool
	for retry := 0; ; retry++ {
		if err := p.cfg.Budget.Acquire(queryCtx, lane); err != nil {
			return SemanticAssetSearchResponse{}, latencyMS, rateLimited, err
		}
		started := p.nowTime()
		response, err := p.port.Search(queryCtx, request)
		latencyMS += maxLatencyMS(0, p.nowTime().Sub(started).Milliseconds())
		if err == nil {
			return response, latencyMS, rateLimited, nil
		}
		limited, ok := providerbudget.AsRateLimited(err)
		if !ok {
			return SemanticAssetSearchResponse{}, latencyMS, rateLimited, err
		}
		rateLimited = true
		p.cfg.Budget.ObserveRateLimit(lane, limited.RetryAfter)
		if retry >= p.cfg.RateLimitRetries || queryCtx.Err() != nil {
			return SemanticAssetSearchResponse{}, latencyMS, rateLimited, err
		}
	}
}

func maxLatencyMS(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func planUnitByCandidate(plan *SemanticProductionPlan, candidateID string) *SemanticProductionUnit {
	if plan == nil {
		return nil
	}
	for index := range plan.Units {
		if plan.Units[index].CandidateID == candidateID {
			return &plan.Units[index]
		}
	}
	return nil
}
