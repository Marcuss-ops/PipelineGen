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

func TestDedupeMaterializedImagesDoesNotPromoteStockBoundCandidates(t *testing.T) {
	image := dedupeTestImage("stock-entity", 5)
	segments := []scriptpkg.VidRushSegmentResult{
		{SegmentID: "stock-scene", Assets: scriptpkg.SegmentAssetSelection{
			Candidates: []scriptpkg.SegmentAssetCandidate{image},
		}},
		{SegmentID: "regular-scene", Assets: scriptpkg.SegmentAssetSelection{
			SecondaryImages: []scriptpkg.SegmentAssetCandidate{image},
			Candidates:      []scriptpkg.SegmentAssetCandidate{image},
		}},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		ImagesPerScene: 1,
		StockBindings:  []scriptpkg.StockBindingInput{{SegmentID: "stock-scene"}},
	}

	dedupeMaterializedImagesAcrossSegments(plan, segments)

	if got := segments[0].Assets.SecondaryImages; len(got) != 0 {
		t.Fatalf("stock-bound scene images = %v, want none", got)
	}
	if got := segments[1].Assets.SecondaryImages; len(got) != 1 || got[0].AssetID != "stock-entity" {
		t.Fatalf("regular scene images = %v, want stock-entity candidate", got)
	}
}
