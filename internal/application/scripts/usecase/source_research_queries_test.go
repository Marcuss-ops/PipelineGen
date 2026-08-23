// Package usecase — source_research_queries_test.go pins the research
// cache fingerprint contract: the fingerprint must fold in the semantic
// version tokens (research pipeline, identity registry, query planner)
// and the injected provider-policy token (provider set + target pool),
// so SearXNG-only and SearXNG+DDG results never share a cache entry.
package usecase

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestResearchFingerprint_ProviderPolicySensitivity(t *testing.T) {
	queries := []string{"Floyd Mayweather Jr. boxing career earnings"}
	policy := scriptpkg.ResearchPolicy{MaxQueries: 4, ResultsPerQuery: 5, MaxPages: 8, MinSources: 3}
	searxngOnly := "provider=searxng,target_pool=8"
	withDDG := "provider=duckduckgo,target_pool=8"
	biggerPool := "provider=searxng,target_pool=12"

	base := researchFingerprint(queries, policy, searxngOnly)

	if got := researchFingerprint(queries, policy, withDDG); got == base {
		t.Error("fingerprint unchanged when the provider policy gains a DDG fallback")
	}
	if got := researchFingerprint(queries, policy, biggerPool); got == base {
		t.Error("fingerprint unchanged when the target pool changes")
	}
	if got := researchFingerprint(queries, policy, ""); got == base {
		t.Error("fingerprint unchanged when the policy version is missing")
	}
	if got := researchFingerprint(queries, policy, searxngOnly); got != base {
		t.Error("fingerprint not deterministic for identical inputs")
	}
	if got := researchFingerprint([]string{"Canelo Alvarez boxing earnings"}, policy, searxngOnly); got == base {
		t.Error("fingerprint unchanged when queries change")
	}
	if got := researchFingerprint(queries, scriptpkg.ResearchPolicy{MaxQueries: 4, ResultsPerQuery: 5, MaxPages: 16, MinSources: 3}, searxngOnly); got == base {
		t.Error("fingerprint unchanged when the research policy changes")
	}
}

func TestResearchCacheIdentity_PolicyVersionConsistency(t *testing.T) {
	src := scriptpkg.SourceSpec{
		Type:        scriptpkg.SourceResearch,
		Topic:       "Floyd Mayweather Jr.",
		Search:      true,
		CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModePreferCache, Version: "test-v1"},
		Research:    scriptpkg.ResearchPolicy{MaxQueries: 4, MaxPages: 8, MinSources: 3},
	}
	_, _, _, _, k1 := researchCacheIdentity(src, "it", "provider=searxng,target_pool=8")
	_, _, _, _, k2 := researchCacheIdentity(src, "it", "provider=searxng,target_pool=8")
	_, _, _, _, k3 := researchCacheIdentity(src, "it", "provider=duckduckgo,target_pool=8")

	if k1 != k2 {
		t.Error("identical policy version must produce the identical cache key")
	}
	if k1 == k3 {
		t.Error("different provider policy must produce different cache keys")
	}
}

func TestResearchCacheIdentity_MetricSensitivity(t *testing.T) {
	base := scriptpkg.SourceSpec{
		Type:        scriptpkg.SourceResearch,
		Topic:       "Canelo Álvarez boxer",
		Search:      true,
		CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModePreferCache, Version: "test-v1"},
		Research:    scriptpkg.ResearchPolicy{MaxQueries: 4, MaxPages: 8, MinSources: 3},
	}
	_, _, _, _, generic := researchCacheIdentity(base, "en", "provider=searxng,target_pool=8")

	netWorth := base
	netWorth.Research.RankingMetric = "estimated_net_worth"
	_, _, _, _, netWorthKey := researchCacheIdentity(netWorth, "en", "provider=searxng,target_pool=8")

	career := base
	career.Research.RankingMetric = "career_earnings"
	_, _, _, _, careerKey := researchCacheIdentity(career, "en", "provider=searxng,target_pool=8")

	if netWorthKey == generic {
		t.Error("net-worth metric must change the candidate cache key vs generic")
	}
	if careerKey == netWorthKey {
		t.Error("career-earnings and net-worth metrics must produce distinct candidate cache keys")
	}
	if careerKey == generic {
		t.Error("career-earnings metric must change the candidate cache key vs generic")
	}
}

func TestResearchAggregateCacheKey_PolicyVersionSensitivity(t *testing.T) {
	src := scriptpkg.SourceSpec{
		Type:        scriptpkg.SourceResearch,
		Topic:       "Boxing legends",
		Search:      true,
		CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModePreferCache, Version: "test-v1"},
		Research:    scriptpkg.ResearchPolicy{MaxPages: 8, MinSources: 3, Candidates: []string{"Tyson", "Hagler"}},
	}
	k1 := researchAggregateCacheKey("Boxing legends", "it", src, "provider=searxng,target_pool=8")
	k2 := researchAggregateCacheKey("Boxing legends", "it", src, "provider=searxng,target_pool=8")
	k3 := researchAggregateCacheKey("Boxing legends", "it", src, "provider=duckduckgo,target_pool=8")

	if k1 != k2 {
		t.Error("identical policy version must produce the identical aggregate key")
	}
	if k1 == k3 {
		t.Error("different provider policy must produce different aggregate keys")
	}
}
