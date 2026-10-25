package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// coordinatorStub is a fixed-result researchSearchCoordinatorPort for
// testing the resolver quality gate without real providers. It emulates
// a coordinator that already applied the subject filter.
type coordinatorStub struct {
	results []CoordinatorSearchResult
}

func (c *coordinatorStub) SearchWithFallback(context.Context, string, []string, int) []CoordinatorSearchResult {
	return c.results
}

// TestWebResearchResolverQualityGateFailsWhenCoordinatorPoolInsufficient
// pins the plan's fail-closed contract: when every provider yields fewer
// subject-valid results than the gate requires, the resolver must fail
// with ErrResearchInsufficientSources, produce no EvidencePack aggregate,
// and write nothing to the cache — even when the cache is enabled.
func TestWebResearchResolverQualityGateFailsWhenCoordinatorPoolInsufficient(t *testing.T) {
	coordinator := &coordinatorStub{results: []CoordinatorSearchResult{
		{Hit: scriptports.WebSearchHit{Title: "Floyd Mayweather Jr. boxing career", URL: "https://example.com/may-1", Content: "Floyd Mayweather boxing career earnings"}, Provider: "searxng", QueryLevel: 0},
		{Hit: scriptports.WebSearchHit{Title: "Floyd Mayweather Jr. business profile", URL: "https://example.com/may-2", Content: "Floyd Mayweather boxing endorsements"}, Provider: "duckduckgo", QueryLevel: 0},
	}}
	search := &researchSearchFake{}
	fetch := &researchFetchFake{text: "Floyd Mayweather Jr. boxing career earnings documented by major publishers."}
	cache := newResearchCountingCache()
	resolver := NewWebResearchResolver(search, fetch)
	resolver.SetCache(cache)
	resolver.SetSearchCoordinator(coordinator)
	if err := resolver.SetResearchRanker(scriptports.ResearchRankerFunc(func(_ context.Context, _ string, _ scriptpkg.RankingMetric, _ []scriptports.ResearchCandidateRankingInput) (scriptports.ResearchRankingResult, error) {
		return scriptports.ResearchRankingResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}

	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:        scriptpkg.SourceResearch,
		Topic:       "Floyd Mayweather Jr.",
		Search:      true,
		Research:    scriptpkg.ResearchPolicy{Candidates: []string{"Floyd Mayweather Jr."}, MaxQueries: 1, MinSources: 3, MaxPages: 5},
		CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModePreferCache},
	}, scriptpkg.SourceResolutionContext{Language: "en"})

	require.ErrorIs(t, err, ErrResearchInsufficientSources,
		"a 2-source pool must fail the MinSources=3 gate")
	require.Zero(t, cache.saves, "quality gate failure must not write any cache entry")
	require.Zero(t, len(search.calls), "coordinator path must not touch the raw searcher")
}
