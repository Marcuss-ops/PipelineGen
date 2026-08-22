package adapters

import (
	"strings"
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
			LegacyFileMD5:  "hash-john-cena", RightsStatus: "unknown_allowed",
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
		{AssetID: "durable", Provider: scriptpkg.VidRushProviderInternetImages, Query: "John Cena", SourceURL: "https://images.example/durable.jpg", Score: 1, DriveLink: "https://drive.google.com/file/d/durable/view", LegacyFileMD5: "hash", RightsStatus: "unknown_allowed", AcquisitionStatus: scriptpkg.VidRushStatusAcquired, VerificationStatus: scriptpkg.VidRushStatusVerified, PersistenceStatus: scriptpkg.VidRushStatusPersisted, IndexStatus: scriptpkg.VidRushStatusIndexed},
	}}}

	got, ok := findEntityImageCandidate(entity, seg)
	if !ok || got.AssetID != "durable" || got.DriveLink == "" {
		t.Fatalf("candidate = %+v, ok=%v; want durable Drive-backed candidate", got, ok)
	}
}

func TestFindEntityImageCandidate_NormalizesEnglishPossessive(t *testing.T) {
	entity := scriptpkg.AnnotatedEntity{CanonicalName: "Dwayne Johnson's", Text: "Dwayne Johnson's", Type: "PERSON"}
	seg := scriptpkg.VidRushSegmentResult{Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
		readyEntityImageCandidate("asset-dwayne", "Dwayne Johnson", "Dwayne Johnson"),
	}}}
	got, ok := findEntityImageCandidate(entity, seg)
	if !ok || got.AssetID != "asset-dwayne" {
		t.Fatalf("possessive entity did not bind canonical candidate: ok=%v asset=%q", ok, got.AssetID)
	}
}

func entityImagePolicyForTest() (policy mediadomain.EntityImagePolicy) {
	policy.Enabled = true
	policy.EntityTypes = []string{"PERSON"}
	return policy
}

// readyEntityImageCandidate builds a fully materialized internet_images
// candidate (acquired → verified → persisted → indexed with a Drive link), the
// minimum bar findEntityImageCandidate requires before it can bind an entity
// image. The identity fields (query/entity) are supplied by each test.
func readyEntityImageCandidate(assetID, query, entity string) scriptpkg.SegmentAssetCandidate {
	return scriptpkg.SegmentAssetCandidate{
		AssetID:            assetID,
		Provider:           scriptpkg.VidRushProviderInternetImages,
		Query:              query,
		Entity:             entity,
		Score:              1,
		SourceURL:          "https://images.example/" + assetID + ".jpg",
		DriveLink:          "https://drive.google.com/file/d/" + assetID + "/view",
		LegacyFileMD5:           "hash-" + assetID,
		RightsStatus:       "unknown_allowed",
		AcquisitionStatus:  scriptpkg.VidRushStatusAcquired,
		VerificationStatus: scriptpkg.VidRushStatusVerified,
		PersistenceStatus:  scriptpkg.VidRushStatusPersisted,
		IndexStatus:        scriptpkg.VidRushStatusIndexed,
	}
}

// TestEntityImage_CandidateMustMatchPrimaryEntity certifies that only a
// candidate whose query/entity is identity-scoped to the primary entity can be
// bound. A candidate retrieved for a generic scene keyword is skipped even when
// it is fully materialized.
func TestEntityImage_CandidateMustMatchPrimaryEntity(t *testing.T) {
	entity := scriptpkg.AnnotatedEntity{CanonicalName: "Dwayne Johnson", Text: "Dwayne Johnson", Type: "PERSON"}
	seg := scriptpkg.VidRushSegmentResult{Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
		readyEntityImageCandidate("asset-wrestling-ring", "wrestling", "wrestling"),
		readyEntityImageCandidate("asset-dwayne", "Dwayne Johnson", "Dwayne Johnson"),
	}}}

	got, ok := findEntityImageCandidate(entity, seg)
	if !ok {
		t.Fatal("expected an identity-scoped candidate to bind")
	}
	if got.AssetID != "asset-dwayne" {
		t.Fatalf("bound candidate = %q, want the Dwayne Johnson identity candidate", got.AssetID)
	}
}

// TestEntityImage_GenericSceneCandidateCannotBecomePersonImage certifies the
// dangerous case from the certification spec: a generic scene candidate (query
// "wrestling") must never be presented as a person image, even when no
// identity-scoped candidate exists.
func TestEntityImage_GenericSceneCandidateCannotBecomePersonImage(t *testing.T) {
	entity := scriptpkg.AnnotatedEntity{CanonicalName: "Dwayne Johnson", Text: "Dwayne Johnson", Type: "PERSON"}
	seg := scriptpkg.VidRushSegmentResult{Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
		readyEntityImageCandidate("asset-wrestling-ring", "wrestling", "wrestling"),
	}}}

	if _, ok := findEntityImageCandidate(entity, seg); ok {
		t.Fatal("generic scene candidate was incorrectly promoted to person image")
	}
}

// TestEntityImage_PersonQueryPreservesEntityIdentity certifies that the
// entity-image query derivation is identity-scoped: the person's canonical
// name is the query, never a generic scene keyword.
func TestEntityImage_PersonQueryPreservesEntityIdentity(t *testing.T) {
	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
		ID: "scene-dwayne", SegmentID: "scene-dwayne", Index: 0,
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			CanonicalName: "Dwayne Johnson", Text: "Dwayne Johnson", Type: "PERSON",
		}}},
	}}}
	seg := scriptpkg.VidRushSegmentResult{SegmentID: "scene-dwayne", SceneID: "scene-dwayne"}

	queries := scenePrimaryEntityQueries(spec, buildSceneIdentityIndex(spec), seg)
	if len(queries) != 1 || queries[0] != "Dwayne Johnson" {
		t.Fatalf("primary entity queries = %v, want exactly [Dwayne Johnson]", queries)
	}
	if strings.Contains(strings.ToLower(queries[0]), "wrestling") {
		t.Fatalf("person query %q must be identity-scoped, not a scene keyword", queries[0])
	}
}

// TestEntityImage_ProjectionBindsOnlyIdentityScopedCandidate certifies the
// end-to-end binding projection: the matching identity candidate resolves,
// while a scene carrying only a generic candidate stays not_found.
func TestEntityImage_ProjectionBindsOnlyIdentityScopedCandidate(t *testing.T) {
	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
		{ID: "scene-a", SegmentID: "scene-a", Index: 0, Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: "Dwayne Johnson", Text: "Dwayne Johnson", Type: "PERSON"}}}},
		{ID: "scene-b", SegmentID: "scene-b", Index: 1, Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: "Dwayne Johnson", Text: "Dwayne Johnson", Type: "PERSON"}}}},
	}}
	segments := []scriptpkg.VidRushSegmentResult{
		{SegmentID: "scene-a", SceneID: "scene-a", Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
			readyEntityImageCandidate("asset-dwayne", "Dwayne Johnson", "Dwayne Johnson"),
		}}},
		{SegmentID: "scene-b", SceneID: "scene-b", Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
			readyEntityImageCandidate("asset-wrestling-ring", "wrestling", "wrestling"),
		}}},
	}

	out := projectEntityImageBindings(spec, segments, entityImagePolicyForTest())

	if img := out.Scenes[0].Annotations.PrimaryEntities[0].Image; img == nil || img.Status != "resolved" || img.AssetID != "asset-dwayne" {
		t.Fatalf("scene-a entity image = %+v, want resolved asset-dwayne", img)
	}
	if img := out.Scenes[1].Annotations.PrimaryEntities[0].Image; img == nil || img.Status != "not_found" {
		t.Fatalf("scene-b entity image = %+v, want not_found (generic candidate must not bind)", img)
	}
}

// TestInternetImages_NoResultDoesNotFabricateImage certifies that a scene with
// no image candidates yields an honest not_found binding — never a fabricated
// entity image with a fake asset id or Drive link.
func TestInternetImages_NoResultDoesNotFabricateImage(t *testing.T) {
	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
		ID: "scene-dwayne", SegmentID: "scene-dwayne", Index: 0,
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			CanonicalName: "Dwayne Johnson", Text: "Dwayne Johnson", Type: "PERSON",
		}}},
	}}}
	segments := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "scene-dwayne", SceneID: "scene-dwayne",
		Assets: scriptpkg.SegmentAssetSelection{}, // zero candidates
	}}

	out := projectEntityImageBindings(spec, segments, entityImagePolicyForTest())

	img := out.Scenes[0].Annotations.PrimaryEntities[0].Image
	if img == nil {
		t.Fatal("expected an honest not_found binding, got nil")
	}
	if img.Status != "not_found" {
		t.Fatalf("entity image status = %q, want not_found", img.Status)
	}
	if img.AssetID != "" || img.DriveLink != "" {
		t.Fatalf("fabricated image: asset_id=%q drive_link=%q, want both empty", img.AssetID, img.DriveLink)
	}
}

// TestInternetImages_OnlyAllowedProviderCanBind certifies that only an
// internet_images candidate can become an entity image. A candidate from any
// other provider is rejected at the binding gate even when its query matches
// the entity and it is fully materialized.
func TestInternetImages_OnlyAllowedProviderCanBind(t *testing.T) {
	entity := scriptpkg.AnnotatedEntity{CanonicalName: "Dwayne Johnson", Text: "Dwayne Johnson", Type: "PERSON"}

	artlist := readyEntityImageCandidate("asset-artlist", "Dwayne Johnson", "Dwayne Johnson")
	artlist.Provider = "artlist"
	artlist.RightsStatus = "verified"

	youtube := readyEntityImageCandidate("asset-youtube", "Dwayne Johnson", "Dwayne Johnson")
	youtube.Provider = "youtube"
	youtube.SourceURL = "https://youtube.com/watch?v=leaked"

	for _, candidate := range []scriptpkg.SegmentAssetCandidate{artlist, youtube} {
		seg := scriptpkg.VidRushSegmentResult{Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{candidate}}}
		if _, ok := findEntityImageCandidate(entity, seg); ok {
			t.Fatalf("provider %q candidate was incorrectly bound as an entity image", candidate.Provider)
		}
	}
}

// TestMaterialization_UnverifiedImageCannotReachBinding certifies that a
// discovered candidate that has not completed acquire→verify→persist can never
// reach the entity-image binding, even when its query matches the person.
func TestMaterialization_UnverifiedImageCannotReachBinding(t *testing.T) {
	entity := scriptpkg.AnnotatedEntity{CanonicalName: "Dwayne Johnson", Text: "Dwayne Johnson", Type: "PERSON"}
	seg := scriptpkg.VidRushSegmentResult{Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
		AssetID: "asset-discovered", Provider: "internet_images", Query: "Dwayne Johnson",
		SourceURL: "https://images.example/dwayne.jpg", Score: 1,
		// No DriveLink/LegacyFileMD5/lifecycle states → still unverified.
	}}}}

	if _, ok := findEntityImageCandidate(entity, seg); ok {
		t.Fatal("unverified candidate was incorrectly bound as an entity image")
	}
}
