package vidrush

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func canonicalReadyEntityImageCandidate(assetID, entity string) scriptpkg.SegmentAssetCandidate {
	return scriptpkg.SegmentAssetCandidate{
		AssetID:            assetID,
		Provider:           scriptpkg.VidRushProviderInternetImages,
		Entity:             entity,
		SourceURL:          "https://images.example/" + assetID + ".jpg",
		SemanticStatus:     "accepted",
		SemanticScore:      0.92,
		AcquisitionStatus:  scriptpkg.VidRushStatusAcquired,
		VerificationStatus: scriptpkg.VidRushStatusVerified,
		PersistenceStatus:  scriptpkg.VidRushStatusPersisted,
		IndexStatus:        "indexed",
		ContentSHA256:      strings.Repeat("a", 64),
		DriveLink:          "https://drive.google.com/file/d/" + assetID + "/view",
		RightsStatus:       "unknown_allowed",
	}
}

func TestEntityImageMatchesCanonicalAlias(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{
		Assets: scriptpkg.SegmentAssetSelection{
			Candidates: []scriptpkg.SegmentAssetCandidate{canonicalReadyEntityImageCandidate("gotti", "John Gotti")},
		},
		Insights: scriptpkg.SegmentInsights{
			ImageEntityCanonicalNames: map[string]string{"Teflon Don": "John Gotti"},
		},
	}
	got := entityImageMatches("Teflon Don", "PERSON", seg)
	if len(got) != 1 || got[0].AssetID != "gotti" {
		t.Fatalf("alias match = %+v, want the John Gotti candidate", got)
	}
}

func TestEntityImageMatchesAllowsOrgAcronymButNotPersonAcronym(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{Assets: scriptpkg.SegmentAssetSelection{
		Candidates: []scriptpkg.SegmentAssetCandidate{canonicalReadyEntityImageCandidate("fbi", "FBI")},
	}}
	if got := entityImageMatches("Federal Bureau of Investigation", "ORG", seg); len(got) != 1 {
		t.Fatalf("ORG acronym match = %+v, want the FBI candidate", got)
	}
	if got := entityImageMatches("Federal Bureau of Investigation", "PERSON", seg); len(got) != 0 {
		t.Fatalf("PERSON acronym match = %+v, want no match", got)
	}
}
