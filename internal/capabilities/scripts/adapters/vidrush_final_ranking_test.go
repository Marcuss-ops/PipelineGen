package adapters

import (
	"context"
	"errors"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"testing"
)

type testCandidateReranker struct {
	results []scriptports.CandidateRerankResult
	err     error
	calls   int
}

func (r *testCandidateReranker) Rerank(_ context.Context, _ scriptports.CandidateRerankRequest) ([]scriptports.CandidateRerankResult, error) {
	r.calls++
	return r.results, r.err
}
func TestRankWithOptionalRerankerOnlyChangesSemanticComponent(t *testing.T) {
	profile := scriptpkg.SegmentSemanticProfile{SegmentID: "s", Topic: "tractor history"}
	input := []scriptpkg.SegmentAssetCandidate{{AssetID: "a", Provider: "youtube", Query: "tractor history", RelevanceScore: .8, TechnicalQualityScore: .7, DurationMs: 8000, ProviderReliability: .8}, {AssetID: "b", Provider: "youtube", Query: "tractor history", RelevanceScore: .8, TechnicalQualityScore: .7, DurationMs: 8000, ProviderReliability: .8}}
	reranker := &testCandidateReranker{results: []scriptports.CandidateRerankResult{{CandidateID: "b", SemanticScore: 1}, {CandidateID: "a", SemanticScore: .1}}}
	got := NewVidRushWindowRanker().RankWithOptionalReranker(context.Background(), reranker, input, profile, 8000)
	if reranker.calls != 1 || got[0].AssetID != "b" {
		t.Fatalf("calls=%d ranked=%+v", reranker.calls, got)
	}
}
func TestRankWithOptionalRerankerFallsBackOnError(t *testing.T) {
	r := &testCandidateReranker{err: errors.New("offline")}
	got := NewVidRushWindowRanker().RankWithOptionalReranker(context.Background(), r, []scriptpkg.SegmentAssetCandidate{{AssetID: "a", Provider: "youtube", Query: "tractor history", RelevanceScore: .9, DurationMs: 8000}}, scriptpkg.SegmentSemanticProfile{SegmentID: "s", Topic: "tractor history"}, 8000)
	if len(got) != 1 || got[0].Score <= 0 {
		t.Fatalf("fallback ranking=%+v", got)
	}
}
