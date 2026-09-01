package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestProfileFromVidRushSegmentUsesCanonicalInsights(t *testing.T) {
	profile := (scriptpkg.VidRushSegmentResult{
		SegmentID: "seg-1", TextHash: "hash", Text: "tractor history",
		SemanticProfile: &scriptpkg.SegmentSemanticProfile{
			SegmentID: "seg-1", TextHash: "hash",
			Entities:    []scriptpkg.ExtractedEntity{{Value: "John Froelich", Type: "PERSON"}},
			VisualTerms: []scriptpkg.WeightedKeyword{{Value: "early gasoline tractor", Confidence: 1}},
			Retrieval:   &scriptpkg.RetrievalIntent{Images: []string{"early tractor"}},
		},
	}).CanonicalSemanticProfile()
	if profile.SegmentID != "seg-1" || len(profile.Entities) != 1 {
		t.Fatalf("unexpected profile: %+v", profile)
	}
	if len(profile.VisualTerms) != 1 || profile.VisualTerms[0].Value != "early gasoline tractor" {
		t.Fatalf("canonical visual terms were not preserved: %+v", profile)
	}
	if profile.Retrieval == nil || len(profile.Retrieval.Images) != 1 || profile.Retrieval.Images[0] != "early tractor" {
		t.Fatalf("canonical image queries were not preserved: %#v", profile.Retrieval)
	}
}
