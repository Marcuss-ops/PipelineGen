package adapters

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/imagegate"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type stubVLMRecorder struct {
	calls  int
	err    error
	result func([]scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate
}

func (s *stubVLMRecorder) ScoreImageCandidates(_ context.Context, _ string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	if s.result != nil {
		return s.result(candidates), nil
	}
	return candidates, nil
}

func TestOptionalEntityImageScorerOffSkipsVLM(t *testing.T) {
	vlm := &stubVLMRecorder{}
	scorer := NewOptionalEntityImageScorer("off", vlm)

	in := []scriptpkg.SegmentAssetCandidate{
		{SourceURL: "https://example.com/steve.jpg", RetrievalProvider: "duckduckgo"},
		{SourceURL: "https://example.com/bad.jpg", SemanticStatus: entitycatalog.CandidateSemanticRejected, QualityReason: "low resolution"},
	}
	got, err := scorer.ScoreImageCandidates(context.Background(), "Steve Jobs", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vlm.calls != 0 {
		t.Fatalf("expected 0 VLM calls in off mode, got %d", vlm.calls)
	}
	if got[0].SemanticStatus != entitycatalog.CandidateSemanticAccepted || !imagegate.ReusablePersonImageVerdict(got[0]) {
		t.Fatalf("expected deterministic acceptance for candidate 0, got %+v", got[0])
	}
	if got[1].SemanticStatus != entitycatalog.CandidateSemanticRejected {
		t.Fatalf("expected rejected candidate to stay rejected, got %+v", got[1])
	}
}

func TestOptionalEntityImageScorerOptionalSkipsVLMForWikipediaAndFallsBackOnError(t *testing.T) {
	vlm := &stubVLMRecorder{err: errors.New("context deadline exceeded")}
	scorer := NewOptionalEntityImageScorer("optional", vlm)

	in := []scriptpkg.SegmentAssetCandidate{
		{SourceURL: "https://upload.wikimedia.org/wikipedia/commons/steve.jpg", RetrievalProvider: "wikimedia_commons"},
		{SourceURL: "https://example.com/steve-web.jpg", RetrievalProvider: "searxng"},
	}
	got, err := scorer.ScoreImageCandidates(context.Background(), "Steve Jobs", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vlm.calls != 1 {
		t.Fatalf("expected 1 VLM call (only for non-wikipedia candidate), got %d", vlm.calls)
	}
	if got[0].SemanticStatus != entitycatalog.CandidateSemanticAccepted || !imagegate.ReusablePersonImageVerdict(got[0]) {
		t.Fatalf("expected wikipedia candidate accepted deterministically, got %+v", got[0])
	}
	if got[1].SemanticStatus != entitycatalog.CandidateSemanticAccepted || !imagegate.ReusablePersonImageVerdict(got[1]) {
		t.Fatalf("expected fallback acceptance on VLM error, got %+v", got[1])
	}
}

func TestOptionalEntityImageScorerOptionalPreservesExplicitVLMRejection(t *testing.T) {
	vlm := &stubVLMRecorder{
		result: func(in []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
			out := append([]scriptpkg.SegmentAssetCandidate(nil), in...)
			out[0].SemanticStatus = entitycatalog.CandidateSemanticRejected
			out[0].QualityReason = "wrong person"
			return out
		},
	}
	scorer := NewOptionalEntityImageScorer("optional", vlm)

	in := []scriptpkg.SegmentAssetCandidate{
		{SourceURL: "https://example.com/other.jpg", RetrievalProvider: "searxng"},
	}
	got, err := scorer.ScoreImageCandidates(context.Background(), "Steve Jobs", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0].SemanticStatus != entitycatalog.CandidateSemanticRejected {
		t.Fatalf("expected explicit VLM rejection to be preserved, got %+v", got[0])
	}
}
