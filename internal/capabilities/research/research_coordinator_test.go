// Package app — research_coordinator_test.go covers the subject-aware
// multi-provider escalation contract of ResearchSearchCoordinator:
// early stop at targetPool, provider fallback on error/timeout,
// cross-provider dedup (URL + host/title), and RequiredTerms/ExcludedTerms
// subject filtering (including diacritic-insensitive identity matching).
package research

import (
	"context"
	"errors"
	"fmt"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// coordinatorTestProvider is a scriptable WebSearchProvider for tests.
// It records every query it is asked, per query, and can fail per query.
type coordinatorTestProvider struct {
	name      string
	responses map[string][]scriptports.WebSearchHit
	errs      map[string]error
	calls     []string
}

func (p *coordinatorTestProvider) Name() string { return p.name }

func (p *coordinatorTestProvider) Search(_ context.Context, query string, limit int) ([]scriptports.WebSearchHit, error) {
	p.calls = append(p.calls, query)
	if err := p.errs[query]; err != nil {
		return nil, err
	}
	hits := p.responses[query]
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// floydHit builds a subject-valid hit for Floyd Mayweather Jr. with a
// distinct title per id, so the second-pass dedup (same host + same
// normalized title) does not collapse test pools.
func floydHit(id string) scriptports.WebSearchHit {
	return scriptports.WebSearchHit{
		Title:   "Floyd Mayweather Jr boxing career " + id,
		URL:     "https://example.com/" + id,
		Content: "Floyd Mayweather boxing career earnings and record",
	}
}

func coordinatorForTest(providers ...scriptports.WebSearchProvider) *ResearchSearchCoordinator {
	return NewResearchSearchCoordinator(
		&SubjectIdentityAdapter{
			Resolve: func(subject string) scriptpkg.SubjectIdentity {
				return usecase.NewSubjectIdentityResolver().Resolve(subject)
			},
		},
		&QueryPlannerAdapter{
			FullPlan: func(identity scriptpkg.SubjectIdentity, maxQueries int) []string {
				return usecase.NewQueryPlanner().FullPlan(identity, maxQueries)
			},
		},
		providers,
		zap.NewNop(),
	)
}

func TestCoordinator_StopsAtTargetPool_ProviderBNotCalled(t *testing.T) {
	searxng := &coordinatorTestProvider{name: "searxng", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	ddg := &coordinatorTestProvider{name: "duckduckgo", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	for i := 0; i < 8; i++ {
		searxng.responses["q1"] = append(searxng.responses["q1"], floydHit(fmt.Sprintf("floyd-%d", i)))
	}
	pool := coordinatorForTest(searxng, ddg).SearchWithFallback(context.Background(), "Floyd Mayweather Jr.", []string{"q1", "q2"}, 8)

	if len(pool) != 8 {
		t.Fatalf("pool size = %d, want 8", len(pool))
	}
	if len(ddg.calls) != 0 {
		t.Fatalf("provider B called %d times, want 0 (target reached with provider A alone)", len(ddg.calls))
	}
	if len(searxng.calls) != 1 {
		t.Fatalf("provider A called %d times, want 1 (q2 must not fire)", len(searxng.calls))
	}
	for _, r := range pool {
		if r.Provider != "searxng" {
			t.Errorf("provider = %q, want searxng", r.Provider)
		}
		if r.QueryLevel != 0 {
			t.Errorf("query_level = %d, want 0", r.QueryLevel)
		}
	}
}

func TestCoordinator_EscalatesToProviderB_UntilTargetPool(t *testing.T) {
	searxng := &coordinatorTestProvider{name: "searxng", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	ddg := &coordinatorTestProvider{name: "duckduckgo", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	for i := 0; i < 2; i++ {
		searxng.responses["q1"] = append(searxng.responses["q1"], floydHit(fmt.Sprintf("searxng-a-%d", i)))
	}
	for i := 2; i < 8; i++ {
		ddg.responses["q1"] = append(ddg.responses["q1"], floydHit(fmt.Sprintf("ddg-b-%d", i)))
	}
	pool := coordinatorForTest(searxng, ddg).SearchWithFallback(context.Background(), "Floyd Mayweather Jr.", []string{"q1", "q2"}, 8)

	if len(pool) != 8 {
		t.Fatalf("pool size = %d, want 8", len(pool))
	}
	if len(searxng.calls) != 1 || len(ddg.calls) != 1 {
		t.Fatalf("calls searxng=%d ddg=%d, want 1/1 (stop once target reached)", len(searxng.calls), len(ddg.calls))
	}
	byProvider := map[string]int{}
	for _, r := range pool {
		byProvider[r.Provider]++
	}
	if byProvider["searxng"] != 2 || byProvider["duckduckgo"] != 6 {
		t.Fatalf("pool split searxng=%d duckduckgo=%d, want 2/6", byProvider["searxng"], byProvider["duckduckgo"])
	}
}

func TestCoordinator_ProviderError_EscalatesToNextProvider(t *testing.T) {
	searxng := &coordinatorTestProvider{name: "searxng", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	ddg := &coordinatorTestProvider{name: "duckduckgo", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	searxng.errs["q1"] = errors.New("searxng down")
	for i := 0; i < 4; i++ {
		ddg.responses["q1"] = append(ddg.responses["q1"], floydHit(fmt.Sprintf("ddg-b-%d", i)))
	}
	pool := coordinatorForTest(searxng, ddg).SearchWithFallback(context.Background(), "Floyd Mayweather Jr.", []string{"q1"}, 8)

	if len(pool) != 4 {
		t.Fatalf("pool size = %d, want 4 (all from provider B)", len(pool))
	}
	if len(searxng.calls) != 1 || len(ddg.calls) != 1 {
		t.Fatalf("calls searxng=%d ddg=%d, want 1/1", len(searxng.calls), len(ddg.calls))
	}
	for _, r := range pool {
		if r.Provider != "duckduckgo" {
			t.Errorf("provider = %q, want duckduckgo", r.Provider)
		}
	}
}

func TestCoordinator_ProviderTimeout_EscalatesToNextProvider(t *testing.T) {
	searxng := &coordinatorTestProvider{name: "searxng", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	ddg := &coordinatorTestProvider{name: "duckduckgo", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	searxng.errs["q1"] = context.DeadlineExceeded
	for i := 0; i < 3; i++ {
		ddg.responses["q1"] = append(ddg.responses["q1"], floydHit(fmt.Sprintf("ddg-b-%d", i)))
	}
	pool := coordinatorForTest(searxng, ddg).SearchWithFallback(context.Background(), "Floyd Mayweather Jr.", []string{"q1"}, 8)

	if len(pool) != 3 {
		t.Fatalf("pool size = %d, want 3 (provider A timed out, B recovered)", len(pool))
	}
	if len(searxng.calls) != 1 || len(ddg.calls) != 1 {
		t.Fatalf("calls searxng=%d ddg=%d, want 1/1", len(searxng.calls), len(ddg.calls))
	}
}

func TestCoordinator_SameURLAcrossProviders_Deduped(t *testing.T) {
	searxng := &coordinatorTestProvider{name: "searxng", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	ddg := &coordinatorTestProvider{name: "duckduckgo", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	searxng.responses["q1"] = []scriptports.WebSearchHit{
		{Title: "Floyd Mayweather boxing", URL: "https://www.example.com/a?utm_source=x&utm_medium=newsletter", Content: "Floyd Mayweather boxing record"},
	}
	ddg.responses["q1"] = []scriptports.WebSearchHit{
		{Title: "Floyd Mayweather boxing", URL: "https://example.com/a", Content: "Floyd Mayweather boxing record"},
	}
	pool := coordinatorForTest(searxng, ddg).SearchWithFallback(context.Background(), "Floyd Mayweather Jr.", []string{"q1"}, 8)

	if len(pool) != 1 {
		t.Fatalf("pool size = %d, want 1 (same normalized URL across providers)", len(pool))
	}
	if pool[0].Provider != "searxng" {
		t.Errorf("provider = %q, want searxng (first occurrence wins)", pool[0].Provider)
	}
}

func TestCoordinator_SameHostSameTitle_Deduped(t *testing.T) {
	searxng := &coordinatorTestProvider{name: "searxng", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	ddg := &coordinatorTestProvider{name: "duckduckgo", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	searxng.responses["q1"] = []scriptports.WebSearchHit{
		{Title: "Floyd Mayweather Jr. Boxing", URL: "https://example.com/a", Content: "Floyd Mayweather boxing record"},
	}
	ddg.responses["q1"] = []scriptports.WebSearchHit{
		{Title: "Floyd Mayweather Jr. Boxing", URL: "https://example.com/b", Content: "Floyd Mayweather boxing record"},
	}
	pool := coordinatorForTest(searxng, ddg).SearchWithFallback(context.Background(), "Floyd Mayweather Jr.", []string{"q1"}, 8)

	if len(pool) != 1 {
		t.Fatalf("pool size = %d, want 1 (same host + same normalized title)", len(pool))
	}
}

func TestCoordinator_SubjectFilter_RequiredAndExcludedTerms(t *testing.T) {
	searxng := &coordinatorTestProvider{name: "searxng", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	searxng.responses["q1"] = []scriptports.WebSearchHit{
		{Title: "George Floyd death", URL: "https://example.com/george", Content: "George Floyd murder trial and protests"},
		{Title: "Floyd Mayweather Jr boxing", URL: "https://example.com/mayweather", Content: "Floyd Mayweather boxing record"},
		{Title: "Career earnings overview", URL: "https://example.com/career", Content: "earnings endorsements career financial history"},
	}
	pool := coordinatorForTest(searxng).SearchWithFallback(context.Background(), "Floyd Mayweather Jr.", []string{"q1"}, 8)

	if len(pool) != 1 {
		t.Fatalf("pool size = %d, want 1 (excluded-term and required-term hits rejected)", len(pool))
	}
	if pool[0].Hit.URL != "https://example.com/mayweather" {
		t.Errorf("kept URL = %q, want mayweather page", pool[0].Hit.URL)
	}
}

func TestCoordinator_AllProvidersInsufficient_ReturnsPool(t *testing.T) {
	searxng := &coordinatorTestProvider{name: "searxng", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	ddg := &coordinatorTestProvider{name: "duckduckgo", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	searxng.responses["q1"] = []scriptports.WebSearchHit{floydHit("a-1"), floydHit("a-2")}
	ddg.responses["q1"] = []scriptports.WebSearchHit{floydHit("b-1")}
	pool := coordinatorForTest(searxng, ddg).SearchWithFallback(context.Background(), "Floyd Mayweather Jr.", []string{"q1"}, 8)

	if len(pool) != 3 {
		t.Fatalf("pool size = %d, want 3 (all providers exhausted below target)", len(pool))
	}
}

func TestCoordinator_EscalatesAcrossQueries_WithQueryLevel(t *testing.T) {
	searxng := &coordinatorTestProvider{name: "searxng", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	ddg := &coordinatorTestProvider{name: "duckduckgo", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	searxng.responses["q1"] = []scriptports.WebSearchHit{floydHit("q1-a")}
	ddg.responses["q1"] = []scriptports.WebSearchHit{floydHit("q1-b")}
	for i := 0; i < 6; i++ {
		searxng.responses["q2"] = append(searxng.responses["q2"], floydHit(fmt.Sprintf("q2-%d", i)))
	}
	ddg.responses["q2"] = []scriptports.WebSearchHit{floydHit("q2-ddg")}

	pool := coordinatorForTest(searxng, ddg).SearchWithFallback(context.Background(), "Floyd Mayweather Jr.", []string{"q1", "q2"}, 8)

	if len(pool) != 8 {
		t.Fatalf("pool size = %d, want 8", len(pool))
	}
	if len(ddg.calls) != 1 || ddg.calls[0] != "q1" {
		t.Fatalf("ddg calls = %v, want [q1] only (q2 target reached via searxng)", ddg.calls)
	}
	levels := map[int]int{}
	for _, r := range pool {
		levels[r.QueryLevel]++
	}
	if levels[0] != 2 || levels[1] != 6 {
		t.Fatalf("query levels = %v, want {0:2, 1:6}", levels)
	}
}

func TestCoordinator_DiacriticIdentity_Matching(t *testing.T) {
	searxng := &coordinatorTestProvider{name: "searxng", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	searxng.responses["q1"] = []scriptports.WebSearchHit{
		{Title: "Canelo Alvarez boxing", URL: "https://example.com/canelo-ascii", Content: "Canelo Alvarez boxing record"},
		{Title: "Canelo Álvarez boxeo", URL: "https://example.com/canelo-accent", Content: "Canelo Álvarez pelea boxeo profesional"},
		{Title: "Pacquiao career", URL: "https://example.com/pacquiao", Content: "Manny Pacquiao career earnings and endorsements"},
	}
	pool := coordinatorForTest(searxng).SearchWithFallback(context.Background(), "Canelo Álvarez", []string{"q1"}, 8)

	if len(pool) != 2 {
		t.Fatalf("pool size = %d, want 2 (accented and ASCII variants both accepted, unrelated hit rejected)", len(pool))
	}
}

func TestCoordinator_DefaultTargetPool_FromSetTargetPool(t *testing.T) {
	searxng := &coordinatorTestProvider{name: "searxng", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	ddg := &coordinatorTestProvider{name: "duckduckgo", responses: map[string][]scriptports.WebSearchHit{}, errs: map[string]error{}}
	for i := 0; i < 8; i++ {
		searxng.responses["q1"] = append(searxng.responses["q1"], floydHit(fmt.Sprintf("floyd-%d", i)))
	}
	c := coordinatorForTest(searxng, ddg)
	c.SetTargetPool(5)
	pool := c.SearchWithFallback(context.Background(), "Floyd Mayweather Jr.", []string{"q1"}, 0)

	if len(pool) != 5 {
		t.Fatalf("pool size = %d, want 5 (default target pool from SetTargetPool)", len(pool))
	}
	if len(ddg.calls) != 0 {
		t.Fatalf("provider B called %d times, want 0", len(ddg.calls))
	}
}
