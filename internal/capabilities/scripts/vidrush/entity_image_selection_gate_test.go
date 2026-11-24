package vidrush

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestFilterEntityImageSelectionCandidatesFailsClosedForOrg(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "org-scene",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{{Value: "FBI", Type: "ORG", Confidence: 0.9}},
		},
	}
	candidate := scriptpkg.SegmentAssetCandidate{
		AssetID:   "fbi",
		Provider:  scriptpkg.VidRushProviderInternetImages,
		Entity:    "FBI",
		SourceURL: "https://images.example/fbi.jpg",
	}
	if got := filterEntityImageSelectionCandidates([]scriptpkg.SegmentAssetCandidate{candidate}, segment, []string{"ORG"}); len(got) != 0 {
		t.Fatalf("unscored ORG candidate was selectable: %+v", got)
	}
	candidate.SemanticStatus = "accepted"
	candidate.SemanticScore = 0.9
	if got := filterEntityImageSelectionCandidates([]scriptpkg.SegmentAssetCandidate{candidate}, segment, []string{"ORG"}); len(got) != 1 {
		t.Fatalf("accepted ORG candidate was dropped: %+v", got)
	}
}
