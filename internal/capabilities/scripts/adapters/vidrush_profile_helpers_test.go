package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestProfileFromVidRushSegmentUsesCanonicalInsights(t *testing.T) {
	profile := profileFromVidRushSegment(scriptpkg.VidRushSegmentResult{
		SegmentID: "seg-1", TextHash: "hash", Text: "tractor history",
		Insights: scriptpkg.SegmentInsights{
			Entities:         []scriptpkg.ExtractedEntity{{Value: "John Froelich", Type: "PERSON"}},
			ImportantWords:   []string{"tractor"},
			ImportantPhrases: []string{"first gasoline tractor"},
			ImageQueries:     []string{"early tractor"},
		},
	})
	if profile.SegmentID != "seg-1" || len(profile.Entities) != 1 || len(profile.Keywords) != 1 {
		t.Fatalf("unexpected profile: %+v", profile)
	}
	if got := profileImageQueries(profile); len(got) != 1 || got[0] != "early tractor" {
		t.Fatalf("image queries = %#v", got)
	}
}

func TestScoreVidRushCandidateUsesProfileSemanticMatch(t *testing.T) {
	profile := scriptpkg.SegmentSemanticProfile{
		Topic:    "early gasoline tractor",
		Keywords: []scriptpkg.WeightedKeyword{{Value: "farm machinery", Confidence: 1}},
	}
	matched := scriptpkg.SegmentAssetCandidate{Provider: scriptpkg.VidRushProviderArtlist, AssetID: "a", Query: "early gasoline tractor farm machinery"}
	unmatched := scriptpkg.SegmentAssetCandidate{Provider: scriptpkg.VidRushProviderArtlist, AssetID: "b", Query: "abstract city skyline"}
	if scoreVidRushCandidateWithProfile(matched, profile, false) <= scoreVidRushCandidateWithProfile(unmatched, profile, false) {
		t.Fatalf("semantic profile did not improve matching candidate")
	}
}
