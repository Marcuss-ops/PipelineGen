package adapters

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestVidRushFanoutPlanPreservesProviderPolicyAndInputs(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title: "scene title",
		MediaPlan: mediadomain.MediaPlanSpec{
			Planner: mediadomain.MediaPlannerPolicy{CandidateLimit: 75},
			ProviderPolicy: mediadomain.MediaProviderPolicy{
				Artlist:        mediadomain.MediaToggleEnabled,
				InternetImages: mediadomain.MediaToggleEnabled,
			},
		},
	}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "segment-1", TextHash: "hash-1", Text: "scene",
		Insights: scriptpkg.SegmentInsights{
			ArtlistQueries: []string{" artlist "}, ImageQueries: []string{" image "},
		},
	}
	fanout := buildVidRushFanoutPlan(plan, segment, &gatedArtlistSearcher{}, &gatedImageSearcher{}, nil)
	if !fanout.artlistEnabled || !fanout.imagesEnabled {
		t.Fatalf("provider enablement = artlist:%v images:%v", fanout.artlistEnabled, fanout.imagesEnabled)
	}
	if fanout.perQueryLimit != 50 {
		t.Fatalf("per-query limit = %d, want capped at 50", fanout.perQueryLimit)
	}
	if len(fanout.artlistQueries) != 1 || fanout.artlistQueries[0] != " artlist " {
		t.Fatalf("artlist queries = %#v, want original query preserved", fanout.artlistQueries)
	}
}

func TestVidRushFanoutMergeKeepsCandidatesWithoutSelectingWinner(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{}
	updated := scriptpkg.VidRushSegmentResult{SegmentID: "segment-1"}
	profile := canonicalSegmentProfile(updated)
	outcome := vidRushProviderOutcome{
		provider: scriptpkg.VidRushProviderArtlist,
		candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "clip-1", Provider: scriptpkg.VidRushProviderArtlist,
			SourceURL: "https://cdn.example/clip-1.m3u8", RelevanceScore: 0.9,
		}},
	}
	if err := mergeVidRushProviderOutcome(&updated, outcome, plan, profile, updated.SegmentID); err != nil {
		t.Fatal(err)
	}
	if len(updated.Assets.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(updated.Assets.Candidates))
	}
	if updated.Assets.PrimaryVideo != nil {
		t.Fatalf("provider discovery selected a primary: %+v", updated.Assets.PrimaryVideo)
	}
}
