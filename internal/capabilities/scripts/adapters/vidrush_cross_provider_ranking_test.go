package adapters

import (
	"context"
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSelectVidRushPrimaryVideoWithPolicyUsesCommonPlanner(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		Planner: mediadomain.MediaPlannerPolicy{Strategy: "deterministic", CandidateLimit: 8},
	}}
	candidates := []scriptpkg.SegmentAssetCandidate{
		durableVideoCandidate(scriptpkg.VidRushProviderArtlist, "artlist-low", .4),
		durableVideoCandidate(scriptpkg.VidRushProviderYouTube, "youtube-high", .95),
	}
	got := selectVidRushPrimaryVideoWithPolicy(candidates, plan, scriptpkg.SegmentSemanticProfile{}, 8000, nil, context.Background())
	if got == nil || got.AssetID != "youtube-high" {
		t.Fatalf("primary=%+v, want common ranker winner", got)
	}
}

func TestSelectVidRushPrimaryVideoWithPolicyHonorsCandidateLimit(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		Planner: mediadomain.MediaPlannerPolicy{Strategy: "deterministic", CandidateLimit: 1},
	}}
	candidates := []scriptpkg.SegmentAssetCandidate{
		durableVideoCandidate(scriptpkg.VidRushProviderArtlist, "a", .6),
		durableVideoCandidate(scriptpkg.VidRushProviderYouTube, "b", .9),
	}
	got := selectVidRushPrimaryVideoWithPolicy(candidates, plan, scriptpkg.SegmentSemanticProfile{}, 8000, nil, context.Background())
	if got == nil || got.AssetID != "b" {
		t.Fatalf("primary=%+v, want highest candidate within policy limit", got)
	}
}
