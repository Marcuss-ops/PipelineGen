package usecase

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type fanoutSearch struct {
	delay  time.Duration
	active atomic.Int32
	peak   atomic.Int32
}

func (s *fanoutSearch) Search(ctx context.Context, query string, _ int) ([]scriptports.WebSearchHit, error) {
	active := s.active.Add(1)
	for {
		peak := s.peak.Load()
		if active <= peak || s.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	defer s.active.Add(-1)
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	slug := strings.NewReplacer(" ", "-", ".", "").Replace(strings.ToLower(query))
	return []scriptports.WebSearchHit{{Title: query, URL: "https://example.com/" + slug, Content: "career earnings business financial history"}}, nil
}

type fanoutFetch struct{ delay time.Duration }

func (f fanoutFetch) Fetch(ctx context.Context, url string, _ int) (scriptports.WebPage, error) {
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return scriptports.WebPage{}, ctx.Err()
	}
	return scriptports.WebPage{URL: url, Title: url, Text: "career earnings business financial history documented by a major publisher"}, nil
}

func TestWebResearchResolverCandidateFanoutIsBoundedAndOrdered(t *testing.T) {
	search := &fanoutSearch{delay: 60 * time.Millisecond}
	resolver := NewWebResearchResolver(search, fanoutFetch{delay: 60 * time.Millisecond})
	candidates := []string{"Floyd Mayweather Jr.", "Canelo Alvarez", "Mike Tyson", "Manny Pacquiao"}
	if err := resolver.SetResearchRanker(scriptports.ResearchRankerFunc(func(_ context.Context, _ string, _ scriptpkg.RankingMetric, inputs []scriptports.ResearchCandidateRankingInput) (scriptports.ResearchRankingResult, error) {
		out := make([]scriptports.ResearchCandidateRanking, len(inputs))
		for i, input := range inputs {
			out[i] = scriptports.ResearchCandidateRanking{CandidateID: input.CandidateID, Rank: len(inputs) - i, Rationale: "editorial ranking fixture"}
		}
		return scriptports.ResearchRankingResult{Ranking: out}, nil
	})); err != nil {
		t.Fatal(err)
	}
	src := scriptpkg.SourceSpec{
		Type:        scriptpkg.SourceResearch,
		Topic:       "The 10 richest boxers",
		Search:      true,
		CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModeDisabled},
		Research:    scriptpkg.ResearchPolicy{Candidates: candidates, MaxParallel: 2, MaxPages: 1, MinSources: 1},
	}

	started := time.Now()
	resolved, err := resolver.Resolve(context.Background(), src, scriptpkg.SourceResolutionContext{ItemID: "fanout", Language: "en"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 360*time.Millisecond {
		t.Fatalf("fan-out was not parallel enough: elapsed=%s", elapsed)
	}
	if got := search.peak.Load(); got > 2 {
		t.Fatalf("max parallel exceeded: got=%d want<=2", got)
	}
	if got := search.peak.Load(); got < 2 {
		t.Fatalf("candidate calls did not overlap: peak=%d", got)
	}
	if resolved.ResearchReport == nil || resolved.ResearchReport.Mode != "multi_candidate" {
		t.Fatalf("missing fan-out report: %#v", resolved.ResearchReport)
	}
	if got, want := resolved.ResearchReport.AcceptedSources, len(candidates); got != want {
		t.Fatalf("accepted sources=%d want=%d", got, want)
	}
	for _, candidate := range candidates {
		marker := fmt.Sprintf("Candidate: %s", candidate)
		if !strings.Contains(resolved.SourceText, marker) {
			t.Fatalf("missing ordered subject marker %q in source text", marker)
		}
	}
	expectedOrder := []string{candidates[3], candidates[2], candidates[1], candidates[0]}
	last := -1
	for _, candidate := range expectedOrder {
		at := strings.Index(resolved.SourceText, fmt.Sprintf("Candidate: %s", candidate))
		if at <= last {
			t.Fatalf("rank order changed around candidate %q", candidate)
		}
		last = at
	}
}

func TestWebResearchResolverCandidateFanoutFailsClosedOnDuplicate(t *testing.T) {
	resolver := NewWebResearchResolver(&fanoutSearch{}, fanoutFetch{})
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type: scriptpkg.SourceResearch, Topic: "ranking", Search: true,
		Research: scriptpkg.ResearchPolicy{Candidates: []string{"Tyson", " tyson "}},
	}, scriptpkg.SourceResolutionContext{})
	if err == nil || !strings.Contains(err.Error(), "duplicate research candidate") {
		t.Fatalf("expected duplicate candidate error, got %v", err)
	}
}

// dropOneSearch returns no results for a configured "weak" candidate so the
// per-candidate evidence gate fails for exactly that subject.
type dropOneSearch struct {
	weak string
}

func (s *dropOneSearch) Search(_ context.Context, query string, _ int) ([]scriptports.WebSearchHit, error) {
	if strings.Contains(strings.ToLower(query), strings.ToLower(s.weak)) {
		return nil, nil
	}
	slug := strings.NewReplacer(" ", "-", ".", "").Replace(strings.ToLower(query))
	return []scriptports.WebSearchHit{{Title: query, URL: "https://example.com/" + slug, Content: "career earnings business financial history"}}, nil
}

// TestWebResearchResolverCandidateFanoutDegradesOnWeakCandidate pins the
// degradable fan-out contract: a candidate below the evidence gate is
// excluded from the ranking (recorded in DroppedCandidates, ranking marked
// uncertain) instead of failing the whole job, while the survivors still
// produce an aggregate pack. If EVERY candidate fails, the fanout still
// fails closed with ErrResearchInsufficientSources.
func TestWebResearchResolverCandidateFanoutDegradesOnWeakCandidate(t *testing.T) {
	candidates := []string{"Floyd Mayweather Jr.", "Mike Tyson", "Weak Name", "Manny Pacquiao"}
	resolver := NewWebResearchResolver(&dropOneSearch{weak: "Weak Name"}, fanoutFetch{})
	if err := resolver.SetResearchRanker(scriptports.ResearchRankerFunc(func(_ context.Context, _ string, _ scriptpkg.RankingMetric, inputs []scriptports.ResearchCandidateRankingInput) (scriptports.ResearchRankingResult, error) {
		out := make([]scriptports.ResearchCandidateRanking, len(inputs))
		for i, input := range inputs {
			out[i] = scriptports.ResearchCandidateRanking{CandidateID: input.CandidateID, Rank: i + 1, Rationale: "fixture"}
		}
		return scriptports.ResearchRankingResult{Ranking: out, Info: scriptpkg.ResearchRankingInfo{
			RequestedMetric: "estimated_net_worth", ResolvedMetric: "estimated_net_worth",
			Strategy: "llm_verified_evidence", CandidatesWithEvidence: len(inputs),
		}}, nil
	})); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:        scriptpkg.SourceResearch,
		Topic:       "richest boxers ranked by estimated net worth",
		Search:      true,
		CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModeDisabled},
		Research:    scriptpkg.ResearchPolicy{Candidates: candidates, MaxQueries: 1, MaxPages: 1, MinSources: 1, RankingMetric: "estimated_net_worth", AllowPartialCandidates: true},
	}, scriptpkg.SourceResolutionContext{ItemID: "degrade", Language: "en"})
	if err != nil {
		t.Fatalf("fanout must degrade on one weak candidate, got error: %v", err)
	}
	report := resolved.ResearchReport
	if report == nil || report.Mode != "multi_candidate" {
		t.Fatalf("missing aggregate report: %#v", report)
	}
	if len(report.DroppedCandidates) != 1 || report.DroppedCandidates[0].CandidateID != "Weak Name" {
		t.Fatalf("expected exactly Weak Name dropped, got %#v", report.DroppedCandidates)
	}
	if report.Ranking == nil || !report.Ranking.Uncertain {
		t.Fatalf("partial ranking must be marked uncertain: %#v", report.Ranking)
	}
	if report.Ranking == nil || report.Ranking.CandidatesWithEvidence != 3 {
		t.Fatalf("candidates_with_evidence must equal survivors (3), got %#v", report.Ranking)
	}
	if got := len(report.Evidence.Candidates); got != 3 {
		t.Fatalf("evidence pack must contain only survivors, got %d", got)
	}

	// All candidates below the gate → still fail closed.
	resolver2 := NewWebResearchResolver(&dropOneSearch{weak: ""}, fanoutFetch{})
	if err := resolver2.SetResearchRanker(scriptports.ResearchRankerFunc(func(_ context.Context, _ string, _ scriptpkg.RankingMetric, _ []scriptports.ResearchCandidateRankingInput) (scriptports.ResearchRankingResult, error) {
		return scriptports.ResearchRankingResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}
	_, err = resolver2.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:        scriptpkg.SourceResearch,
		Topic:       "rankings",
		Search:      true,
		CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModeDisabled},
		Research:    scriptpkg.ResearchPolicy{Candidates: []string{"Nobody A", "Nobody B"}, MaxQueries: 1, MaxPages: 1, MinSources: 2},
	}, scriptpkg.SourceResolutionContext{ItemID: "all-drop", Language: "en"})
	if err == nil || !strings.Contains(err.Error(), "RESEARCH_INSUFFICIENT_SOURCES") {
		t.Fatalf("all-candidates-failed must still fail closed, got %v", err)
	}
}

// metricAwareSearch records every query the raw searcher receives so a test
// can assert the fan-out produced clean metric-aware queries.
type metricAwareSearch struct {
	mu      sync.Mutex
	queries []string
}

func (s *metricAwareSearch) Search(_ context.Context, query string, _ int) ([]scriptports.WebSearchHit, error) {
	s.mu.Lock()
	s.queries = append(s.queries, query)
	s.mu.Unlock()
	slug := strings.NewReplacer(" ", "-", ".", "").Replace(strings.ToLower(query))
	return []scriptports.WebSearchHit{{Title: query, URL: "https://example.com/" + slug, Content: "career earnings business financial history"}}, nil
}

func (s *metricAwareSearch) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

// TestWebResearchResolverCandidateFanoutMetricAwareQueries pins the fan-out
// contract that a ranking metric produces clean per-candidate queries —
// "Canelo Álvarez estimated net worth", not the redundant
// "Canelo Álvarez boxing estimated net worth" that leaked in when the seed
// query carried a "boxing" suffix into identity resolution.
func TestWebResearchResolverCandidateFanoutMetricAwareQueries(t *testing.T) {
	search := &metricAwareSearch{}
	resolver := NewWebResearchResolver(search, fanoutFetch{})
	if err := resolver.SetResearchRanker(scriptports.ResearchRankerFunc(func(_ context.Context, _ string, _ scriptpkg.RankingMetric, inputs []scriptports.ResearchCandidateRankingInput) (scriptports.ResearchRankingResult, error) {
		out := make([]scriptports.ResearchCandidateRanking, len(inputs))
		for i, input := range inputs {
			out[i] = scriptports.ResearchCandidateRanking{CandidateID: input.CandidateID, Rank: i + 1, Rationale: "fixture"}
		}
		return scriptports.ResearchRankingResult{Ranking: out}, nil
	})); err != nil {
		t.Fatal(err)
	}

	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:        scriptpkg.SourceResearch,
		Topic:       "richest boxers ranked by net worth",
		Search:      true,
		CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModeDisabled},
		Research:    scriptpkg.ResearchPolicy{Candidates: []string{"Canelo Alvarez"}, MaxQueries: 4, MaxPages: 1, MinSources: 1, RankingMetric: "estimated_net_worth"},
	}, scriptpkg.SourceResolutionContext{ItemID: "metric-aware", Language: "en"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	queries := search.snapshot()
	if len(queries) == 0 {
		t.Fatal("no queries recorded")
	}
	joined := strings.Join(queries, "\n")
	for _, q := range queries {
		if strings.Contains(q, "boxing") {
			t.Errorf("metric-aware query must not carry a redundant 'boxing' qualifier: %q", q)
		}
	}
	if !strings.Contains(joined, "Canelo Álvarez estimated net worth") {
		t.Errorf("expected 'Canelo Álvarez estimated net worth' in queries, got:\n%s", joined)
	}
}

// TestWebResearchResolverFanoutPropagatesRankingQuality pins that the
// per-candidate MetricEvidenceQuality / MetricClaimCount and the granular
// ranking_confidence produced by the ranker survive into the evidence pack
// and the aggregate report (so a single weak candidate stays visible without
// marking the whole ranking uncertain).
func TestWebResearchResolverFanoutPropagatesRankingQuality(t *testing.T) {
	resolver := NewWebResearchResolver(&fanoutSearch{}, fanoutFetch{})
	if err := resolver.SetResearchRanker(scriptports.ResearchRankerFunc(func(_ context.Context, _ string, _ scriptpkg.RankingMetric, inputs []scriptports.ResearchCandidateRankingInput) (scriptports.ResearchRankingResult, error) {
		out := make([]scriptports.ResearchCandidateRanking, len(inputs))
		for i, input := range inputs {
			quality := scriptpkg.MetricEvidenceQualityHigh
			if input.CandidateID == "Canelo Alvarez" {
				quality = scriptpkg.MetricEvidenceQualityLow
			}
			out[i] = scriptports.ResearchCandidateRanking{CandidateID: input.CandidateID, Rank: i + 1, Score: 100, Rationale: "fixture", MetricEvidenceQuality: quality, MetricClaimCount: i + 1}
		}
		return scriptports.ResearchRankingResult{
			Ranking: out,
			Info: scriptpkg.ResearchRankingInfo{
				RequestedMetric: "estimated_net_worth", ResolvedMetric: "estimated_net_worth",
				Strategy:   "deterministic_verified_financial_evidence",
				Confidence: &scriptpkg.ResearchRankingConfidence{ComparableCandidates: len(inputs), TotalCandidates: len(inputs), Coverage: 1.0},
			},
		}, nil
	})); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:        scriptpkg.SourceResearch,
		Topic:       "richest boxers ranked by net worth",
		Search:      true,
		CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModeDisabled},
		Research:    scriptpkg.ResearchPolicy{Candidates: []string{"Floyd Mayweather Jr.", "Canelo Alvarez"}, MaxQueries: 1, MaxPages: 1, MinSources: 1, RankingMetric: "estimated_net_worth"},
	}, scriptpkg.SourceResolutionContext{ItemID: "quality-prop", Language: "en"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	pack := resolved.ResearchEvidence
	if pack == nil || len(pack.Candidates) != 2 {
		t.Fatalf("expected 2 candidates in evidence pack, got %#v", pack)
	}
	byID := map[string]scriptpkg.RankedResearchCandidate{}
	for _, candidate := range pack.Candidates {
		byID[candidate.CandidateID] = candidate
	}
	if got := byID["Floyd Mayweather Jr."]; got.MetricEvidenceQuality != scriptpkg.MetricEvidenceQualityHigh || got.MetricClaimCount != 1 {
		t.Errorf("Mayweather quality/claim count not propagated: quality=%q claimCount=%d", got.MetricEvidenceQuality, got.MetricClaimCount)
	}
	if got := byID["Canelo Alvarez"]; got.MetricEvidenceQuality != scriptpkg.MetricEvidenceQualityLow || got.MetricClaimCount != 2 {
		t.Errorf("Canelo quality/claim count not propagated: quality=%q claimCount=%d", got.MetricEvidenceQuality, got.MetricClaimCount)
	}

	report := resolved.ResearchReport
	if report == nil || report.Ranking == nil || report.Ranking.Confidence == nil {
		t.Fatalf("ranking_confidence not persisted in aggregate report: %#v", report)
	}
	if report.Ranking.Confidence.ComparableCandidates != 2 || report.Ranking.Confidence.Coverage != 1.0 {
		t.Errorf("ranking_confidence mismatch: %#v", report.Ranking.Confidence)
	}
}
