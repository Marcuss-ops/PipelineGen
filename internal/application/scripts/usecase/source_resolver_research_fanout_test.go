package usecase

import (
	"context"
	"fmt"
	"strings"
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
	if err := resolver.SetResearchRanker(scriptports.ResearchRankerFunc(func(_ context.Context, _ string, inputs []scriptports.ResearchCandidateRankingInput) ([]scriptports.ResearchCandidateRanking, error) {
		out := make([]scriptports.ResearchCandidateRanking, len(inputs))
		for i, input := range inputs {
			out[i] = scriptports.ResearchCandidateRanking{CandidateID: input.CandidateID, Rank: len(inputs) - i, Rationale: "editorial ranking fixture"}
		}
		return out, nil
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
