package scriptgeneration

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func verifiedEntityCandidate(entity, assetID string) scriptpkg.SegmentAssetCandidate {
	return scriptpkg.SegmentAssetCandidate{
		Provider:           scriptpkg.VidRushProviderInternetImages,
		Entity:             entity,
		Query:              entity,
		AssetID:            assetID,
		ContentSHA256:      strings.Repeat("a", 64),
		SourceURL:          "https://example.invalid/" + assetID + ".jpg",
		DriveLink:          "https://drive.google.com/file/d/" + assetID + "/view",
		AcquisitionStatus:  scriptpkg.VidRushStatusAcquired,
		VerificationStatus: scriptpkg.VidRushStatusVerified,
		PersistenceStatus:  scriptpkg.VidRushStatusPersisted,
		IndexStatus:        "INDEXED",
		RightsStatus:       "verified",
		MIMEType:           "image/jpeg",
	}
}

func TestEntityImageBindingAcceptsVerifiedPlaceWithoutSemanticVerdict(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{Assets: scriptpkg.SegmentAssetSelection{
		Candidates: []scriptpkg.SegmentAssetCandidate{verifiedEntityCandidate("Siberia", "siberia")},
	}}
	binding := entityImageBindingFor("Siberia", "GPE", seg)
	if binding == nil {
		t.Fatal("verified GPE image with no semantic verdict must bind")
	}
	if binding.PreviewURL == "" || binding.SHA256 == "" {
		t.Fatalf("binding lost its render material: %+v", binding)
	}
}

func TestEntityImageBindingStillRejectsUnjudgedPerson(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{Assets: scriptpkg.SegmentAssetSelection{
		Candidates: []scriptpkg.SegmentAssetCandidate{verifiedEntityCandidate("Francis Kaufmann", "kaufmann")},
	}}
	if binding := entityImageBindingFor("Francis Kaufmann", "PERSON", seg); binding != nil {
		t.Fatalf("an unjudged PERSON image must fail closed, got %+v", binding)
	}
}

func TestEntityImageBindingAcceptsEnrichedAgencyQueryForAcronym(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{Assets: scriptpkg.SegmentAssetSelection{
		Candidates: []scriptpkg.SegmentAssetCandidate{verifiedEntityCandidate("ANSA news agency", "ansa")},
	}}
	binding := entityImageBindingFor("ANSA", "ORG", seg)
	if binding == nil {
		t.Fatal("an ANSA news-agency candidate must bind back to the ANSA annotation")
	}
}
