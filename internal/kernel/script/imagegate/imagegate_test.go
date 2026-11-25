package imagegate

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func acceptedCandidate() scriptpkg.SegmentAssetCandidate {
	return scriptpkg.SegmentAssetCandidate{
		SemanticStatus: scriptpkg.CandidateSemanticAccepted,
		SourceURL:      "https://example.com/photo.jpg",
	}
}

func TestCandidateEligibleForSlotRejectsWatermarkVerdict(t *testing.T) {
	candidate := acceptedCandidate()
	candidate.HasWatermark = true
	if CandidateEligibleForSlot(candidate, false) {
		t.Fatal("a candidate with an explicit watermark verdict must never fill a slot")
	}
}

func TestCandidateEligibleForSlotRejectsWatermarkedStockPreview(t *testing.T) {
	for _, source := range []string{
		"https://media.gettyimages.com/id/123/photo.jpg",
		"https://www.shutterstock.com/image-photo/watermarked-preview.jpg",
		"https://example.com/watermark/photo.jpg",
		"https://example.com/comp/photo.jpg",
	} {
		candidate := acceptedCandidate()
		candidate.SourceURL = source
		if CandidateEligibleForSlot(candidate, false) {
			t.Fatalf("watermarked source %q must be rejected", source)
		}
	}
}

func TestCandidateEligibleForSlotKeepsCleanAcceptedCandidate(t *testing.T) {
	if !CandidateEligibleForSlot(acceptedCandidate(), false) {
		t.Fatal("a clean accepted candidate must still fill a scene slot")
	}
}
