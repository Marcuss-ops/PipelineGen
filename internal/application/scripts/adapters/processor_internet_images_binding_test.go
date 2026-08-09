package adapters

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestProjectEntityImageBindings_StripsPromptPrefixFromEntity(t *testing.T) {
	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
		ID: "scene-0", SegmentID: "john-cena", Index: 0,
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			CanonicalName: "Describe John Cena", Text: "Describe John Cena", Type: "PERSON",
		}}},
	}}}
	segments := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "john-cena", SceneID: "scene-0",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "asset-john-cena", Provider: scriptpkg.VidRushProviderInternetImages,
			Query: "John Cena", SourceURL: "https://images.example/john-cena.jpg", Score: 1,
			DriveLink: "https://drive.google.com/file/d/john-cena/view",
			FileHash:  "hash-john-cena", RightsStatus: "unknown_allowed",
			AcquisitionStatus:  scriptpkg.VidRushStatusAcquired,
			VerificationStatus: scriptpkg.VidRushStatusVerified,
			PersistenceStatus:  scriptpkg.VidRushStatusPersisted,
			IndexStatus:        scriptpkg.VidRushStatusIndexed,
		}}},
	}}

	got := projectEntityImageBindings(spec, segments, entityImagePolicyForTest())
	image := got.Scenes[0].Annotations.PrimaryEntities[0].Image
	if image == nil || image.Status != "resolved" || image.AssetID != "asset-john-cena" || image.DriveLink == "" {
		t.Fatalf("entity image binding = %+v, want resolved persisted Drive image", image)
	}
}

func TestFindEntityImageCandidate_PrefersDurableCandidate(t *testing.T) {
	entity := scriptpkg.AnnotatedEntity{CanonicalName: "Describe John Cena", Type: "PERSON"}
	seg := scriptpkg.VidRushSegmentResult{Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
		{AssetID: "discovered", Provider: scriptpkg.VidRushProviderInternetImages, Query: "John Cena", SourceURL: "https://images.example/discovered.jpg", Score: 1},
		{AssetID: "durable", Provider: scriptpkg.VidRushProviderInternetImages, Query: "John Cena", SourceURL: "https://images.example/durable.jpg", Score: 1, DriveLink: "https://drive.google.com/file/d/durable/view", FileHash: "hash", RightsStatus: "unknown_allowed", AcquisitionStatus: scriptpkg.VidRushStatusAcquired, VerificationStatus: scriptpkg.VidRushStatusVerified, PersistenceStatus: scriptpkg.VidRushStatusPersisted, IndexStatus: scriptpkg.VidRushStatusIndexed},
	}}}

	got, ok := findEntityImageCandidate(entity, seg)
	if !ok || got.AssetID != "durable" || got.DriveLink == "" {
		t.Fatalf("candidate = %+v, ok=%v; want durable Drive-backed candidate", got, ok)
	}
}

func entityImagePolicyForTest() (policy mediadomain.EntityImagePolicy) {
	policy.Enabled = true
	policy.EntityTypes = []string{"PERSON"}
	return policy
}
