package scriptgeneration

import (
	"context"
	"fmt"
	generationcontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/generation"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/pkg/providerbudget"
)

// dispatch runs one attempt per pending composition through a bounded worker
// pool. A worker owns its candidate's ladder end to end: after recording an
// outcome it enqueues the next rung of the SAME candidate. Different candidates
// are independent and may run at once.
func (p *SemanticSearchBatchPlanner) dispatch(ctx context.Context, plan *SemanticProductionPlan, initial []SemanticSearchAttempt, language, planFingerprint string, forceRefresh bool) ([]SemanticSearchAttemptResult, string) {
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
				row, next := p.runAttempt(ctx, plan, attempt, language, planFingerprint, forceRefresh)
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
func (p *SemanticSearchBatchPlanner) runAttempt(ctx context.Context, plan *SemanticProductionPlan, attempt SemanticSearchAttempt, language, planFingerprint string, forceRefresh bool) (SemanticSearchAttemptResult, *SemanticSearchAttempt) {
	row := SemanticSearchAttemptResult{
		CandidateID: attempt.CandidateID, SceneID: attempt.SceneID,
		Query: attempt.Query, QueryKind: attempt.QueryKind, QueryOrigin: attempt.QueryOrigin, TemplateID: attempt.TemplateID,
		Illustrative: attempt.Illustrative, Rung: attempt.Rung,
		Rank: attempt.Rank, Class: attempt.Class, Entity: attempt.Entity,
	}
	key := cacheKey(attempt, language, attempt.Class.ProviderLane(), p.cfg.Limit, planFingerprint)
	response, cached, latency, rateLimited, searchErr := p.fetch(ctx, attempt, language, key, forceRefresh)
	// The planner cache may still hold a response produced before the
	// placeholder filter existed. Filter after fetch so cached and fresh
	// responses obey the same contract: an SVG/icon/spacer is never a resolved
	// documentary image.
	response = filterSemanticPlaceholderHits(response)
	response = rankSemanticAssetHits(response)
	row.Cached = cached
	row.LatencyMS = latency
	row.RateLimited = rateLimited

	best, hasVerified := bestVerifiedSemanticHit(response.Hits)
	outcome := SemanticSearchOutcome{CandidateID: attempt.CandidateID, Rung: attempt.Rung}
	switch {
	case searchErr != nil:
		row.Status = "error"
		row.ProviderError = true
		row.Error = searchErr.Error()
		outcome.Error = row.Error
	case !hasVerified:
		row.Status = "miss"
		outcome.Error = "provider returned no pixel-verified candidate"
	default:
		row.Status = "hit"
		row.Hits = len(response.Hits)
		row.Provider = strings.TrimSpace(response.Provider)
		outcome.Found = true
		outcome.AssetID = strings.TrimSpace(best.AssetID)
		outcome.VerifierVersion = strings.TrimSpace(best.VerifierVersion)
		for _, hit := range response.Hits {
			if id := strings.TrimSpace(hit.AssetID); id != "" {
				row.AssetIDs = append(row.AssetIDs, id)
			}
		}
		if outcome.AssetID == "" {
			outcome.AssetID = strings.TrimSpace(best.URL)
		}
	}

	p.planMu.Lock()
	_, _, recordErr := plan.RecordSearchOutcome(outcome)
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
			provider := strings.TrimSpace(best.Provider)
			if provider == "" {
				provider = strings.TrimSpace(response.Provider)
			}
			unit.ResolvedImage = &generationcontract.SemanticResolvedImage{
				AssetID: outcome.AssetID, URL: strings.TrimSpace(best.URL),
				DriveLink: strings.TrimSpace(best.DriveLink), SHA256: strings.TrimSpace(best.SHA256),
				MediaType: strings.TrimSpace(best.MediaType), Provider: provider,
				VerifierVersion: strings.TrimSpace(best.VerifierVersion),
				Query:           attempt.Query, QueryOrigin: attempt.QueryOrigin, TemplateID: attempt.TemplateID,
				Illustrative: attempt.Illustrative, Entity: strings.TrimSpace(best.Entity),
				Width: best.Width, Height: best.Height,
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

// isVerifiedSemanticHit is the ONE admission rule for a retrieval result. A
// provider order, a query match or a downloaded image is not identity
// evidence: the verifier must have stamped an accepted verdict with a named
// policy and a content address.
func isVerifiedSemanticHit(hit SemanticAssetHit) bool {
	return strings.EqualFold(strings.TrimSpace(hit.SemanticStatus), "accepted") &&
		strings.TrimSpace(hit.VerifierVersion) != "" &&
		digest.IsSHA256(hit.SHA256)
}

// bestVerifiedSemanticHit returns the best admissible hit after
// rankSemanticAssetHits. It never falls back to Hits[0]: an unverified first
// result is not a candidate.
func bestVerifiedSemanticHit(hits []SemanticAssetHit) (SemanticAssetHit, bool) {
	for _, hit := range hits {
		if isVerifiedSemanticHit(hit) {
			return hit, true
		}
	}
	return SemanticAssetHit{}, false
}

// rankSemanticAssetHits applies the same evidence ordering to fresh and cached
// answers. Provider order is not an editorial quality signal. Explicitly
// rejected candidates are removed; accepted visual verdicts, relevance,
// semantic score, technical quality and provider score break ties in that
// order. Stable sorting preserves upstream order only when evidence is equal.
func rankSemanticAssetHits(response SemanticAssetSearchResponse) SemanticAssetSearchResponse {
	kept := make([]SemanticAssetHit, 0, len(response.Hits))
	for _, hit := range response.Hits {
		// Search results are untrusted candidates. Only a pixel-level accepted
		// verdict with a named verifier may satisfy a plan or enter its cache.
		if isVerifiedSemanticHit(hit) {
			kept = append(kept, hit)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if accepted := strings.EqualFold(strings.TrimSpace(a.SemanticStatus), "accepted"); accepted != strings.EqualFold(strings.TrimSpace(b.SemanticStatus), "accepted") {
			return accepted
		}
		for _, pair := range [][2]float64{{a.RelevanceScore, b.RelevanceScore}, {a.SemanticScore, b.SemanticScore}, {a.TechnicalQualityScore, b.TechnicalQualityScore}, {a.Score, b.Score}} {
			if pair[0] != pair[1] {
				return pair[0] > pair[1]
			}
		}
		return false
	})
	response.Hits = kept
	return response
}

// fetch serves the answer from the plan cache when possible and otherwise
// performs one budgeted provider call. The keyed lock collapses duplicate
// same-entity fetches inside a single run; different entities stay independent.
func (p *SemanticSearchBatchPlanner) fetch(ctx context.Context, attempt SemanticSearchAttempt, language, key string, forceRefresh ...bool) (SemanticAssetSearchResponse, bool, int64, bool, error) {
	bypassCache := len(forceRefresh) > 0 && forceRefresh[0]
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
	if !bypassCache {
		if entry, ok := p.cfg.Cache.Get(ctx, key); ok && p.nowTime().Sub(entry.StoredAt) <= cacheTTL {
			return entry.Response, true, 0, false, nil
		}
	}
	response, latency, rateLimited, err := p.searchWithBudget(ctx, attempt, language)
	if err == nil && hasVerifiedSemanticAssetHit(response) {
		storedAt := p.nowTime()
		p.cfg.Cache.Put(ctx, key, SemanticSearchCacheEntry{
			Response: response, StoredAt: storedAt, ExpiresAt: storedAt.Add(cacheTTL),
			Provider: cacheAnswerProvider(response), ProviderLane: attempt.Class.ProviderLane(),
		})
	}
	return response, false, latency, rateLimited, err
}

func hasVerifiedSemanticAssetHit(response SemanticAssetSearchResponse) bool {
	for _, hit := range response.Hits {
		if isVerifiedSemanticHit(hit) {
			return true
		}
	}
	return false
}

// filterSemanticPlaceholderHits removes vector-icon and spacer assets from a
// planner response. This is the final gate before SemanticsResolvedImage is
// created, so a cached historical response cannot reintroduce them.
func filterSemanticPlaceholderHits(response SemanticAssetSearchResponse) SemanticAssetSearchResponse {
	if len(response.Hits) == 0 {
		return response
	}
	kept := make([]SemanticAssetHit, 0, len(response.Hits))
	for _, hit := range response.Hits {
		if isPlaceholderSemanticAssetURL(hit.URL) {
			continue
		}
		kept = append(kept, hit)
	}
	response.Hits = kept
	return response
}

func isPlaceholderSemanticAssetURL(rawURL string) bool {
	u := strings.ToLower(strings.TrimSpace(rawURL))
	if u == "" {
		return false
	}
	if strings.HasPrefix(u, "data:") {
		return true
	}
	path := u
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if strings.HasSuffix(path, ".svg") {
		return true
	}
	for _, marker := range []string{
		"lucide-static", "/devicons/", "/icons/", "icon-", "placeholder",
		"spacer.", "/1x1.", "pixel.gif", "transparent.png",
	} {
		if strings.Contains(u, marker) {
			return true
		}
	}
	return false
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
		Entity: attempt.Entity, QueryClass: string(attempt.Class), SceneContext: attempt.Evidence, ProviderLane: lane,
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

// semanticPlanFingerprint hashes the immutable planning inputs. It is
// computed once before dispatch: execution mutates unit status and resolved
// images, and those runtime fields must not change a cache identity.
func semanticPlanFingerprint(plan *SemanticProductionPlan) string {
	if plan == nil {
		return ""
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "%s\x00%#v\x00", plan.Schema, plan.Identity)
	for _, unit := range plan.Units {
		_, _ = fmt.Fprintf(&b, "%s|%s|%d|%g|%s|%s|%s|%s|%d|%d|%s|%t|", unit.CandidateID, unit.Recipe,
			unit.Rank, unit.Priority, unit.SceneID, strings.Join(unit.SceneIDs, ","),
			strings.Join(unit.EntityIDs, ","), strings.Join(unit.FactIDs, ","), unit.Start, unit.End,
			unit.AssetKey, unit.RequiresMedia)
		for _, query := range unit.Queries {
			_, _ = fmt.Fprintf(&b, "%s=%s;", query.Kind, query.Text)
		}
	}
	return digest.SHA256Bytes([]byte(b.String()))[:16]
}

func planUnitByCandidate(plan *SemanticProductionPlan, candidateID string) *generationcontract.SemanticProductionUnit {
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
