package adapters

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSelectExactVidRushImagesEqualScoresAreOrderIndependent(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 2, MediaPlan: media.MediaPlanSpec{
		ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
	}}
	first := readyImageCandidateForSelection("first", "same subject", .8)
	first.ContentSHA256 = "a-hash"
	second := readyImageCandidateForSelection("second", "same subject", .8)
	second.ContentSHA256 = "b-hash"

	forward := selectExactVidRushImages([]scriptpkg.SegmentAssetCandidate{first, second}, 2, plan)
	reverse := selectExactVidRushImages([]scriptpkg.SegmentAssetCandidate{second, first}, 2, plan)
	if len(forward) != 2 || len(reverse) != 2 {
		t.Fatalf("selected counts = %d/%d, want 2/2", len(forward), len(reverse))
	}
	if forward[0].AssetID != "first" || reverse[0].AssetID != "first" {
		t.Fatalf("first selected = %q/%q, want content-hash order (first)", forward[0].AssetID, reverse[0].AssetID)
	}
}
