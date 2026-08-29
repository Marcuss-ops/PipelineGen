package adapters

import (
	"context"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type closedYouTubeReranker struct{ selected int }

func (r closedYouTubeReranker) Rerank(_ context.Context, req YouTubeRerankRequest) (int, error) {
	if len(req.Candidates) == 0 {
		return -1, nil
	}
	return r.selected, nil
}

func TestPreRankYouTubeCandidatesCapsAtEightAndPreservesWindows(t *testing.T) {
	candidates := make([]scriptpkg.SegmentAssetCandidate, 10)
	for i := range candidates {
		candidates[i] = scriptpkg.SegmentAssetCandidate{
			SourceURL:       "https://www.youtube.com/watch?v=video-1",
			SourceStartMs:   int64(i * 10000),
			SourceEndMs:     int64((i + 1) * 10000),
			DurationMs:      10000,
			Score:           float64(i) / 10,
			SelectionReason: "transcript candidate",
		}
	}
	got := preRankYouTubeCandidates(candidates, "query", "subject", 20)
	if len(got) != 8 {
		t.Fatalf("got %d candidates, want top 8", len(got))
	}
	if got[0].SourceStartMs != 90000 || got[0].SourceEndMs != 100000 {
		t.Fatalf("top candidate window=%+v", got[0])
	}
	for _, candidate := range got {
		if candidate.DurationMs != candidate.SourceEndMs-candidate.SourceStartMs {
			t.Fatalf("window changed during pre-ranking: %+v", candidate)
		}
	}
}

func TestYouTubeCandidateLimitAlwaysCapsAtEight(t *testing.T) {
	if got := youtubeCandidateLimit(scriptports.VidRushSearchRequest{Limit: 20}); got != 8 {
		t.Fatalf("limit=%d, want 8", got)
	}
}
