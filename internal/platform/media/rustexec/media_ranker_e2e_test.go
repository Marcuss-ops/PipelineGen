package rustexec

import (
	"context"
	"os"
	"testing"
)

// TestMediaRankerRealWorkerReproducesTheGoDecision runs the real mediasampler
// binary through the Go adapter. It is opt-in because the pinned
// `bin/mediasampler` release predates the `rank_candidates` operation: point
// VELOX_RUST_MEDIASAMPLER_PATH at a binary built from this tree.
//
//	VELOX_RUST_MEDIASAMPLER_PATH=$PWD/BrainRust/target/debug/mediasampler \
//	  go test ./internal/platform/media/rustexec -run TestMediaRankerRealWorker -count=1 -v
func TestMediaRankerRealWorkerReproducesTheGoDecision(t *testing.T) {
	binary := os.Getenv("VELOX_RUST_MEDIASAMPLER_PATH")
	if binary == "" {
		t.Skip("VELOX_RUST_MEDIASAMPLER_PATH is not set; the pinned mediasampler release predates rank_candidates")
	}
	executor := newRealExecutor(t, binary, resolveFFmpegBinary())
	adapter, err := NewMediaRankerAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}

	// The diversity fixture from contract/media-ranker/v1: `asset-strong`
	// repeats the prior source and is penalised to 0.625, `asset-tied` lands
	// on the same score, and the pick swaps to the non-consecutive source.
	outcome, err := adapter.RankCandidates(context.Background(), MediaRankerRequest{
		SceneDurationMS: 4000,
		PrevVideoID:     "asset-strong",
		SlotKind:        "primary_video",
		Candidates: []MediaRankerCandidate{
			{
				CandidateID: "candidate-asset-strong", AssetID: "asset-strong",
				Provider: "artlist", MediaType: "video", DurationMS: 4000,
				RightsStatus: "verified", DiscoveryStatus: "searched", MaterializationStatus: "warm",
				Binding: &MediaRankerBinding{
					ID: "binding-strong", AssetID: "asset-strong",
					ApprovalStatus: "approved",
					ManualScore:    1.0, SemanticScore: 1.0, QualityScore: 1.0, SuccessScore: 1.0,
				},
			},
			{
				CandidateID: "candidate-asset-tied", AssetID: "asset-tied",
				Provider: "artlist", MediaType: "video", DurationMS: 4000,
				RightsStatus: "verified", DiscoveryStatus: "searched", MaterializationStatus: "warm",
				Binding: &MediaRankerBinding{
					ID: "binding-tied", AssetID: "asset-tied",
					SemanticScore: 1.0, QualityScore: 0.0, SuccessScore: 0.0,
				},
			},
			{
				CandidateID: "candidate-asset-cold", AssetID: "asset-cold",
				MediaType: "video", DurationMS: 4000,
				RightsStatus: "verified", DiscoveryStatus: "searched", MaterializationStatus: "cold",
			},
		},
	})
	if err != nil {
		t.Fatalf("real rank_candidates failed: %v", err)
	}

	if len(outcome.Ranked) != 2 {
		t.Fatalf("ranked=%d, want 2 (the cold candidate is gated out)", len(outcome.Ranked))
	}
	if outcome.Ranked[0].AssetID != "asset-strong" || outcome.Ranked[0].FinalScore != 0.625 {
		t.Fatalf("top=%+v, want asset-strong at 0.625", outcome.Ranked[0])
	}
	if outcome.Ranked[1].AssetID != "asset-tied" || outcome.Ranked[1].FinalScore != 0.625 {
		t.Fatalf("second=%+v, want asset-tied at 0.625", outcome.Ranked[1])
	}
	if outcome.Winner == nil || outcome.Winner.AssetID != "asset-tied" {
		t.Fatalf("winner=%+v, want the non-consecutive asset-tied", outcome.Winner)
	}
	if len(outcome.Drops) != 1 || outcome.Drops[0].Gate != "available_media" {
		t.Fatalf("drops=%+v, want one available_media gate", outcome.Drops)
	}
}
