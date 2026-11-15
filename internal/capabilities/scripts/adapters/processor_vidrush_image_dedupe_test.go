package adapters

import (
	"fmt"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func dedupeTestImage(id string, n int) scriptpkg.SegmentAssetCandidate {
	return scriptpkg.SegmentAssetCandidate{
		AssetID:       id,
		Provider:      scriptpkg.VidRushProviderInternetImages,
		SourceURL:     "https://images.example/" + id + ".jpg",
		DriveLink:     "https://drive.example/" + id,
		ContentSHA256: fmt.Sprintf("%064x", n),
	}
}

func TestDedupeMaterializedImagesAcrossSegmentsRefillsFromCandidates(t *testing.T) {
	shared := dedupeTestImage("shared", 1)
	firstOnly := dedupeTestImage("first-only", 2)
	secondOnly := dedupeTestImage("second-only", 3)
	spare := dedupeTestImage("spare", 4)
	segments := []scriptpkg.VidRushSegmentResult{
		{SegmentID: "scene-0", Assets: scriptpkg.SegmentAssetSelection{
			SecondaryImages: []scriptpkg.SegmentAssetCandidate{shared, firstOnly},
			Candidates:      []scriptpkg.SegmentAssetCandidate{shared, firstOnly},
		}},
		{SegmentID: "scene-1", Assets: scriptpkg.SegmentAssetSelection{
			SecondaryImages: []scriptpkg.SegmentAssetCandidate{shared, secondOnly},
			Candidates:      []scriptpkg.SegmentAssetCandidate{shared, secondOnly, spare},
		}},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 2}

	dedupeMaterializedImagesAcrossSegments(plan, segments)

	if len(segments[0].Assets.SecondaryImages) != 2 || len(segments[1].Assets.SecondaryImages) != 2 {
		t.Fatalf("counts = %d/%d, want 2/2",
			len(segments[0].Assets.SecondaryImages), len(segments[1].Assets.SecondaryImages))
	}
	if got := segments[1].Assets.SecondaryImages; got[0].AssetID != "second-only" || got[1].AssetID != "spare" {
		t.Fatalf("later scene images = %v, want second-only + spare (shared removed, slot refilled)", got)
	}
}
