package ranking

import (
	"context"
	"os"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"go.uber.org/zap"
)

// TestMediaPoolRankerBridge_RealWorker exercises the exact production path —
// PoolRankRequest → rustexec wire → mediasampler::rank → PoolRankOutcome — so
// the cutover is verified against the worker, not only against a stub.
//
// Opt-in: set VELOX_RUST_MEDIASAMPLER_PATH to a mediasampler binary that
// supports rank_candidates (the pinned bin/mediasampler does).
func TestMediaPoolRankerBridge_RealWorker(t *testing.T) {
	path := os.Getenv("VELOX_RUST_MEDIASAMPLER_PATH")
	if path == "" {
		t.Skip("set VELOX_RUST_MEDIASAMPLER_PATH to run the real-worker ranker e2e")
	}
	bridge, err := NewMediaPoolRankerBridge(path, zap.NewNop())
	if err != nil {
		t.Fatalf("build bridge: %v", err)
	}

	plain := func(assetID, key string, bonus float64) mediamemory.PoolRankCandidate {
		return mediamemory.PoolRankCandidate{
			Key:   key,
			Bonus: bonus,
			Filtered: mediamemory.FilteredCandidate{
				Candidate: mediamemory.MediaCandidate{
					AssetID:               assetID,
					Provider:              "artlist",
					MediaType:             "video",
					DurationMs:            4000,
					CandidateScore:        0.5,
					RightsStatus:          mediamemory.RightsVerified,
					DiscoveryStatus:       mediamemory.DiscoverySearched,
					MaterializationStatus: mediamemory.MaterializationWarm,
				},
			},
		}
	}
	bound := plain("asset-bound", "k-bound", 0)
	bound.Filtered.Binding = mediamemory.MediaBinding{
		ID:             "binding-bound",
		AssetID:        "asset-bound",
		StartMs:        1000,
		EndMs:          5000,
		ApprovalStatus: mediamemory.ApprovalApproved,
		ManualScore:    1.0,
		SemanticScore:  1.0,
		QualityScore:   1.0,
		SuccessScore:   1.0,
	}
	denied := plain("asset-denied", "k-denied", 0)
	denied.Filtered.Candidate.RightsStatus = mediamemory.RightsDenied

	outcome, err := bridge.RankPool(context.Background(), mediamemory.PoolRankRequest{
		SceneDurationMS: 4000,
		SlotKind:        mediamemory.SlotKind("any"),
		Candidates: []mediamemory.PoolRankCandidate{
			plain("asset-aa", "k-aa", 0),
			plain("asset-zz", "k-zz", 0.01),
			bound,
			denied,
		},
	})
	if err != nil {
		t.Fatalf("rank pool: %v", err)
	}

	if len(outcome.Drops) != 1 || outcome.Drops[0].CandidateID != "k-denied" {
		t.Fatalf("denied candidate must be dropped by the license gate, got %+v", outcome.Drops)
	}
	if outcome.Drops[0].Gate != "valid_license" {
		t.Errorf("drop gate = %q, want valid_license", outcome.Drops[0].Gate)
	}

	if len(outcome.Ranked) != 3 {
		t.Fatalf("ranked %d candidates, want 3: %+v", len(outcome.Ranked), outcome.Ranked)
	}
	if outcome.Ranked[0].CandidateID != "k-bound" {
		t.Errorf("winning candidate = %q, want the approved binding (%+v)",
			outcome.Ranked[0].CandidateID, outcome.Ranked)
	}
	if outcome.Winner == nil || outcome.Winner.CandidateID != "k-bound" {
		t.Errorf("winner = %+v, want k-bound", outcome.Winner)
	}
	// The bonus breaks the tie between two otherwise identical candidates in
	// favour of the preferred one, even though its AssetID sorts later.
	if outcome.Ranked[1].CandidateID != "k-zz" || outcome.Ranked[2].CandidateID != "k-aa" {
		t.Errorf("bonus did not break the tie: %+v", outcome.Ranked)
	}
	for _, ranked := range outcome.Ranked {
		if ranked.CandidateID == "" {
			t.Errorf("ranker left a ranking without a pool key: %+v", ranked)
		}
	}
}
