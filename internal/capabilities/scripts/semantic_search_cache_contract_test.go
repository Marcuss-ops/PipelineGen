package scriptgeneration

import (
	"context"
	"errors"
	"testing"
)

type failingSemanticSearchPort struct{ calls int }

func (p *failingSemanticSearchPort) Search(context.Context, SemanticAssetSearchRequest) (SemanticAssetSearchResponse, error) {
	p.calls++
	return SemanticAssetSearchResponse{}, errors.New("provider unavailable")
}

func TestSemanticCacheKeyFencesProviderLimitAndPlan(t *testing.T) {
	attempt := SemanticSearchAttempt{CandidateID: "c1", Entity: "Pavia", Query: "Pavia Italia", Class: SearchClassLocation}
	base := cacheKey(attempt, "it", SearchClassLocation.ProviderLane(), 8, "plan-a")
	cases := map[string]string{
		"provider": cacheKey(attempt, "it", "duckduckgo", 8, "plan-a"),
		"limit":    cacheKey(attempt, "it", SearchClassLocation.ProviderLane(), 9, "plan-a"),
		"plan":     cacheKey(attempt, "it", SearchClassLocation.ProviderLane(), 8, "plan-b"),
	}
	for name, got := range cases {
		if got == base {
			t.Fatalf("%s change did not change the cache key", name)
		}
	}
}

func TestSemanticProviderFailureIsTypedAndNotAMiss(t *testing.T) {
	port := &failingSemanticSearchPort{}
	planner, err := NewSemanticSearchBatchPlanner(port, SemanticSearchBatchConfig{MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	plan := schedulerPlan(t, schedulerIdentities(), planCandidate(
		"vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01",
		SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"},
	))
	report, err := planner.Run(context.Background(), plan, "it")
	var providerErr *SemanticSearchProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("Run error = %v, want SemanticSearchProviderError", err)
	}
	if report.Metrics.Errors == 0 || report.Metrics.Misses != 0 || report.ProviderErrors == 0 {
		t.Fatalf("provider failure report = %+v", report.Metrics)
	}
}
