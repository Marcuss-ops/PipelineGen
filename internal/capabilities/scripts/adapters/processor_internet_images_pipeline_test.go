package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestInternetImageCandidatePipeline_NormalizesFiltersDeduplicatesAndRanks(t *testing.T) {
	got := runInternetImageCandidatePipeline(internetImagePipelineInput{
		Query: "maya ruins",
		Candidates: []scriptpkg.SegmentAssetCandidate{
			{AssetID: "low", Provider: "internet_images", SourceURL: "https://img/low", Score: 0.2},
			{AssetID: "high", Provider: "", SourceURL: "https://img/high", Score: 0.9},
			{AssetID: "high", Provider: "internet_images", SourceURL: "https://img/high", Score: 0.1},
			{AssetID: "youtube", Provider: "youtube", SourceURL: "https://youtube.com/watch?v=x", Score: 1},
		},
	})

	if len(got) != 2 {
		t.Fatalf("pipeline returned %d candidates, want 2: %+v", len(got), got)
	}
	if got[0].AssetID != "high" || got[1].AssetID != "low" {
		t.Fatalf("pipeline order = [%s, %s], want [high, low]", got[0].AssetID, got[1].AssetID)
	}
	for _, candidate := range got {
		if candidate.Provider != "internet_images" {
			t.Fatalf("provider = %q, want internet_images", candidate.Provider)
		}
		if candidate.Query != "maya ruins" {
			t.Fatalf("query = %q, want maya ruins", candidate.Query)
		}
	}
}

func TestInternetImageCandidatePipelineIsDeterministicOnScoreTies(t *testing.T) {
	got := runInternetImageCandidatePipeline(internetImagePipelineInput{Candidates: []scriptpkg.SegmentAssetCandidate{
		{AssetID: "b", Provider: "internet_images", SourceURL: "https://img/b", Score: 0.5},
		{AssetID: "a", Provider: "internet_images", SourceURL: "https://img/a", Score: 0.5},
	}})
	if len(got) != 2 || got[0].AssetID != "a" || got[1].AssetID != "b" {
		t.Fatalf("tie order = %+v, want a then b", got)
	}
}
