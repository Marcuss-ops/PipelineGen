// Package retrieved — provider_selection.go owns the three selection
// contracts layered on top of the RetrievalProvider fallback chain:
//
//   - SearchBest: the concurrent, QUALITY-ranked replacement for the
//     sequential first-hit-wins cascade. Every provider is queried in
//     parallel, so the total wall time is bounded by the slowest provider
//     instead of the sum, and a provider that returns a poor result can no
//     longer hide a better one that runs later in the chain (for example a
//     Wikipedia still with no usable dimensions hiding a SearXNG/DuckDuckGo
//     hit for a sports scene).
//
//   - SearchReceding: query recession. When the primary query returns empty
//     (flaky provider, quota, an over-narrow query), the caller recedes to
//     the next ordered query — or to a relaxed variant produced by
//     RelaxImageQuery — instead of leaving the scene at zero images.
//
//   - HealthReport: the preflight surface. Callers verify provider health
//     BEFORE spending a durable run, so a degraded provider is discovered
//     up-front rather than by burning a full generation.
//
// The three contracts share the quality scoring helpers at the bottom of this
// file. Splitting them out of provider_registry.go keeps both files well under
// max_lines_per_file_strict (godlike/08).
package retrieved

import (
	"context"
	"sort"
	"strings"
	"time"

	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// providerSearchOutcome carries one provider's result set through the
// concurrent fan-out. index preserves the registered fallback order so the
// final ranking is deterministic when two providers score identically.
type providerSearchOutcome struct {
	index    int
	provider detail.ImageProvider
	results  []RetrievalSearchResult
	err      error
}

// SearchBest queries every registered provider CONCURRENTLY and returns the
// UNION of their hits, ranked by quality and de-duplicated.
//
// This is the canonical replacement for the sequential first-hit-wins cascade
// and for "return only the single best provider" selection:
//
//   - Latency is bounded by the slowest provider, not the sum of all of them.
//   - Every provider contributes its depth, so a poor early provider neither
//     wins by arrival order nor hides a better later one. This is what gives
//     the caller a real candidate basin (the volume a ~100-image run needs)
//     instead of the first-hit stone.
//   - Results are ordered by resultQualityScore (upstream score + resolution
//     reward + a small known-license trust bonus), with provider registration
//     order and upstream order as deterministic tie-breaks, then de-duplicated
//     by identity (URL/asset id) so the same image from two providers appears
//     once.
//
// Provider errors are logged and skipped, exactly like SearchAll: one flaky
// source must never abort the others. Returns nil, nil when no provider
// produced a hit.
func (r *RetrievalProviderRegistry) SearchBest(ctx context.Context, query string, opts RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	if r == nil || len(r.providers) == 0 {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	// Concurrent fan-out via the sanctioned primitive (raw goroutines are
	// banned in this package by the architecture gate). Provider errors are
	// folded into the outcome instead of returned, so one flaky source never
	// cancels its siblings.
	outcomes, _ := concurrent.Map(ctx, r.providers, len(r.providers), func(callCtx context.Context, index int, p RetrievalProvider) (providerSearchOutcome, error) {
		results, err := p.Search(callCtx, query, opts)
		return providerSearchOutcome{index: index, provider: p.Name(), results: results, err: err}, nil
	})

	// Flatten the union. provider carries the registration index and order the
	// upstream position, so ties resolve deterministically without relying on
	// map iteration or provider completion order.
	type rankedResult struct {
		result   RetrievalSearchResult
		score    float64
		provider int
		order    int
	}
	ranked := make([]rankedResult, 0)
	order := 0
	for _, outcome := range outcomes {
		if outcome.err != nil {
			if r.log != nil {
				r.log.Warn("retrieval provider errored — excluded from the union",
					zap.String("provider", string(outcome.provider)),
					zap.String("query", query),
					zap.Error(outcome.err),
				)
			}
			continue
		}
		for _, result := range outcome.results {
			ranked = append(ranked, rankedResult{result: result, score: resultQualityScore(result), provider: outcome.index, order: order})
			order++
		}
	}
	if len(ranked) == 0 {
		return nil, nil
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].provider != ranked[j].provider {
			return ranked[i].provider < ranked[j].provider
		}
		return ranked[i].order < ranked[j].order
	})
	out := make([]RetrievalSearchResult, 0, len(ranked))
	seen := make(map[string]struct{}, len(ranked))
	for _, entry := range ranked {
		key := retrievalResultIdentity(entry.result)
		if key != "" {
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, entry.result)
	}
	if r.log != nil {
		r.log.Debug("retrieval union ranked",
			zap.String("query", query),
			zap.Int("providers_consulted", len(r.providers)),
			zap.Int("candidates", len(out)),
		)
	}
	return out, nil
}

// retrievalResultIdentity is the de-duplication key for a union: the first
// non-empty URL/asset locator, lower-cased. Two providers returning the same
// image collapse to one entry; a result without any locator is never collapsed
// (it is kept, since identity cannot be established).
func retrievalResultIdentity(result RetrievalSearchResult) string {
	for _, value := range []string{result.PreviewURL, result.ImageURL, result.ThumbnailURL, result.PageURL, result.SourcePageURL, result.AssetID} {
		if value = strings.TrimSpace(value); value != "" {
			return strings.ToLower(value)
		}
	}
	return ""
}

// SearchReceding walks an ORDERED query ladder and returns the first query
// that produces a non-empty result set, together with the query text that
// succeeded (empty string when every query missed).
//
// It is the explicit rejection of "fanout=0 means the scene stays empty": a
// caller that already has more than one candidate query — or that can derive
// relaxed variants with RelaxImageQuery — must recede instead of failing the
// whole run on the first empty primary search. Each rung is evaluated with
// SearchBest, so a rung also benefits from the concurrent quality selection.
func (r *RetrievalProviderRegistry) SearchReceding(ctx context.Context, queries []string, opts RetrievalSearchOptions) ([]RetrievalSearchResult, string, error) {
	for _, raw := range queries {
		query := strings.TrimSpace(raw)
		if query == "" {
			continue
		}
		results, err := r.SearchBest(ctx, query, opts)
		if err != nil {
			return nil, "", err
		}
		if len(results) > 0 {
			return results, query, nil
		}
	}
	return nil, "", nil
}

// ProviderHealthReport is the preflight projection of a registry probe. A
// provider with a nil error is usable; an unhealthy provider names the reason
// so the operator sees the complete picture in one pass (mirroring the
// media-preflight \"collect ALL failures\" contract).
type ProviderHealthReport struct {
	Healthy   []detail.ImageProvider          `json:"healthy"`
	Unhealthy map[detail.ImageProvider]string `json:"unhealthy"`
	WallMS    int64                           `json:"wall_ms"`
}

// AllHealthy reports whether at least one provider is usable. A registry where
// EVERY provider is unhealthy is a hard preflight failure: the run would
// otherwise spend a full generation only to discover it has no retrieval
// source at all.
func (r ProviderHealthReport) AllHealthy() bool { return len(r.Healthy) > 0 }

// HealthReport probes every registered provider CONCURRENTLY and returns the
// per-provider health summary. It never fails: a probe error is provider-local
// and is reported in Unhealthy.
func (r *RetrievalProviderRegistry) HealthReport(ctx context.Context) ProviderHealthReport {
	started := time.Now()
	report := ProviderHealthReport{Unhealthy: map[detail.ImageProvider]string{}}
	if r == nil || len(r.providers) == 0 {
		report.WallMS = time.Since(started).Milliseconds()
		return report
	}
	type probeOutcome struct {
		provider detail.ImageProvider
		err      error
	}
	outcomes, _ := concurrent.Map(ctx, r.providers, len(r.providers), func(callCtx context.Context, _ int, p RetrievalProvider) (probeOutcome, error) {
		return probeOutcome{provider: p.Name(), err: p.Healthy(callCtx)}, nil
	})
	for _, outcome := range outcomes {
		if outcome.err != nil {
			report.Unhealthy[outcome.provider] = outcome.err.Error()
			continue
		}
		report.Healthy = append(report.Healthy, outcome.provider)
	}
	sort.Slice(report.Healthy, func(i, j int) bool { return report.Healthy[i] < report.Healthy[j] })
	report.WallMS = time.Since(started).Milliseconds()
	return report
}

// HealthyProviders returns the usable providers in registered fallback order.
// Callers that must not query a degraded source (or that want to skip the
// per-provider error path during a durable run) filter with this before
// searching. A nil registry returns nil.
func (r *RetrievalProviderRegistry) HealthyProviders(ctx context.Context) []RetrievalProvider {
	if r == nil || len(r.providers) == 0 {
		return nil
	}
	report := r.HealthReport(ctx)
	healthy := make(map[detail.ImageProvider]struct{}, len(report.Healthy))
	for _, provider := range report.Healthy {
		healthy[provider] = struct{}{}
	}
	out := make([]RetrievalProvider, 0, len(report.Healthy))
	for _, p := range r.providers {
		if _, ok := healthy[p.Name()]; ok {
			out = append(out, p)
		}
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────
// Query recession helpers
// ────────────────────────────────────────────────────────────────────────

// imageQueryInstructionPrefixes are the model/editorial instruction prefixes
// that leak into retrieval queries (\"Describe Musk\", \"image of Tesla\"). They
// are non-semantic and are stripped by the first relaxation rung.
var imageQueryInstructionPrefixes = []string{
	"describe ", "image of ", "photo of ", "picture of ", "a photo of ", "an image of ",
}

// RelaxImageQuery returns an ORDERED ladder of query variants, most specific
// first, for recession when a provider returns nothing. Variants are derived
// deterministically (no model call):
//
//  1. the trimmed original query;
//  2. the query with a leading instruction prefix removed;
//  3. the query with a trailing parenthetical or quoted clause removed;
//  4. progressively shorter leading-token truncations (down to one token),
//     which trade specificity for recall.
//
// Empty and duplicate variants are dropped while preserving order.
func RelaxImageQuery(query string) []string {
	base := strings.Join(strings.Fields(strings.TrimSpace(query)), " ")
	if base == "" {
		return nil
	}
	candidates := []string{base}
	lower := strings.ToLower(base)
	for _, prefix := range imageQueryInstructionPrefixes {
		if strings.HasPrefix(lower, prefix) {
			candidates = append(candidates, strings.TrimSpace(base[len(prefix):]))
			break
		}
	}
	candidates = append(candidates, stripImageQueryClause(base))
	// Leading-token truncation ladder, from MOST specific to most relaxed: keep
	// one fewer leading token each rung until a single token remains. A named
	// subject usually stays at the head, so the tail is the safe part to shed,
	// and the caller recedes toward recall only as far as it must.
	words := strings.Fields(base)
	for end := len(words) - 1; end >= 1; end-- {
		candidates = append(candidates, strings.Join(words[:end], " "))
	}

	out := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.Join(strings.Fields(strings.TrimSpace(candidate)), " ")
		if candidate == "" {
			continue
		}
		key := strings.ToLower(candidate)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

// stripImageQueryClause removes a trailing parenthetical and any trailing
// quoted clause from a query. Both are common editorial decorations that
// narrow a provider search without adding retrievable signal.
func stripImageQueryClause(query string) string {
	trimmed := strings.TrimSpace(query)
	if open := strings.LastIndex(trimmed, "("); open > 0 && strings.HasSuffix(trimmed, ")") {
		trimmed = strings.TrimSpace(trimmed[:open])
	}
	trimmed = strings.TrimSpace(strings.Trim(trimmed, "\"'“”"))
	if cut := strings.LastIndexAny(trimmed, "–—-"); cut > 0 {
		trimmed = strings.TrimSpace(trimmed[:cut])
	}
	return trimmed
}

// ────────────────────────────────────────────────────────────────────────
// Quality scoring
// ────────────────────────────────────────────────────────────────────────

// resultQualityScore is the deterministic ranking key for one candidate:
// upstream score, plus a resolution reward, plus a small trust bonus for a
// known license. The license bonus is deliberately bounded so that a licensed
// but tiny Wikipedia thumbnail cannot outrank a high-resolution
// SearXNG/DuckDuckGo hit.
func resultQualityScore(result RetrievalSearchResult) float64 {
	score := result.Score
	if score <= 0 {
		score = 0.5
	}
	score += resolutionReward(result.Width, result.Height)
	if licenseKnown(result.License) {
		score += 0.25
	}
	return score
}

// resolutionReward maps a candidate's pixel count onto [0,1], saturating at
// 1080p. Unknown dimensions (0×0) earn no reward but are not penalised.
func resolutionReward(width, height int) float64 {
	if width <= 0 || height <= 0 {
		return 0
	}
	const fullHDPixels = float64(1920 * 1080)
	reward := (float64(width) * float64(height)) / fullHDPixels
	if reward > 1 {
		return 1
	}
	return reward
}

// licenseKnown reports whether a provider supplied a real license. \"Unknown\",
// empty and the placeholder values used by the scrapers are all unknown.
func licenseKnown(license string) bool {
	switch strings.ToLower(strings.TrimSpace(license)) {
	case "", "unknown", "n/a", "none":
		return false
	default:
		return true
	}
}
