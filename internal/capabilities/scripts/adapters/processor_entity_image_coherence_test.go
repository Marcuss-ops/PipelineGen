package adapters

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestEntityImage_WrongPersonImageNeverBinds certifies Test 18's dangerous
// case: a scene about Elon Musk must never receive Cody Rhodes' image, even
// when that image is a fully-materialized identity-scoped candidate for a
// *different* person. The binding is keyed on the primary entity's canonical
// identity, so another person's name is not a match.
func TestEntityImage_WrongPersonImageNeverBinds(t *testing.T) {
	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
		ID: "scene-elon", SegmentID: "scene-elon", Index: 0,
		Text: "Elon Musk unveiled the new Tesla prototype.",
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{ID: "entity-elon-musk", Text: "Elon Musk", CanonicalName: "Elon Musk", Type: "PERSON"},
		}},
	}}}
	segments := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "scene-elon", SceneID: "scene-elon",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
			// A wrong-person candidate (identity-scoped, durable, but for a
			// different entity) must never win.
			readyEntityImageCandidate("asset-cody-rhodes", "Cody Rhodes", "Cody Rhodes"),
			readyEntityImageCandidate("asset-elon-musk", "Elon Musk", "Elon Musk"),
		}},
	}}

	out := projectEntityImageBindings(spec, segments, entityImagePolicyForTest())

	img := out.Scenes[0].Annotations.PrimaryEntities[0].Image
	if img == nil || img.Status != "resolved" {
		t.Fatalf("entity image = %+v, want resolved", img)
	}
	if img.AssetID != "asset-elon-musk" {
		t.Fatalf("bound asset_id = %q, want asset-elon-musk", img.AssetID)
	}
	if img.AssetID == "asset-cody-rhodes" {
		t.Fatal("Elon Musk scene was bound to Cody Rhodes' image")
	}
}

// TestEntityImage_SceneWithOnlyWrongPersonStaysNotFound certifies that a scene
// whose only candidates belong to a different person resolves to not_found —
// it must never substitute the wrong identity's image.
func TestEntityImage_SceneWithOnlyWrongPersonStaysNotFound(t *testing.T) {
	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
		ID: "scene-elon", SegmentID: "scene-elon", Index: 0,
		Text: "Elon Musk unveiled the new Tesla prototype.",
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{ID: "entity-elon-musk", Text: "Elon Musk", CanonicalName: "Elon Musk", Type: "PERSON"},
		}},
	}}}
	segments := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "scene-elon", SceneID: "scene-elon",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
			readyEntityImageCandidate("asset-cody-rhodes", "Cody Rhodes", "Cody Rhodes"),
		}},
	}}

	out := projectEntityImageBindings(spec, segments, entityImagePolicyForTest())

	img := out.Scenes[0].Annotations.PrimaryEntities[0].Image
	if img == nil || img.Status != "not_found" {
		t.Fatalf("entity image = %+v, want not_found", img)
	}
	if img.AssetID != "" || img.DriveLink != "" {
		t.Fatalf("wrong image fabricated: asset_id=%q drive_link=%q, want both empty", img.AssetID, img.DriveLink)
	}
}

// TestEntityImage_AssociationCarriesEntitySceneAssetIDs certifies that the
// selection is verified through the full (entity_id, asset_id, scene_id)
// triple — not merely through an image URL. The scene keeps its id, each
// entity keeps its id, and every entity binds only its own asset.
func TestEntityImage_AssociationCarriesEntitySceneAssetIDs(t *testing.T) {
	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
		ID: "scene-elon", SegmentID: "scene-elon", Index: 0,
		Text: "Elon Musk unveiled the new Tesla prototype.",
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{ID: "entity-elon-musk", Text: "Elon Musk", CanonicalName: "Elon Musk", Type: "PERSON"},
			{ID: "entity-tesla", Text: "Tesla", CanonicalName: "Tesla", Type: "ORG"},
		}},
	}}}
	segments := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "scene-elon", SceneID: "scene-elon",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
			readyEntityImageCandidate("asset-tesla", "Tesla", "Tesla"),
			readyEntityImageCandidate("asset-elon-musk", "Elon Musk", "Elon Musk"),
			readyEntityImageCandidate("asset-cody-rhodes", "Cody Rhodes", "Cody Rhodes"),
		}},
	}}

	// Empty EntityTypes lets PERSON/ORG/GPE all bind (the default surface).
	policy := entityImagePolicyForTest()
	policy.EntityTypes = nil
	out := projectEntityImageBindings(spec, segments, policy)

	scene := out.Scenes[0]
	if scene.ID != "scene-elon" {
		t.Fatalf("scene id = %q, want scene-elon", scene.ID)
	}

	type binding struct {
		entityID string
		sceneID  string
		assetID  string
		status   string
	}
	got := map[string]binding{}
	for _, e := range scene.Annotations.PrimaryEntities {
		b := binding{entityID: e.ID, sceneID: scene.ID, status: "none"}
		if e.Image != nil {
			b.assetID = e.Image.AssetID
			b.status = e.Image.Status
		}
		got[e.ID] = b
	}

	elon := got["entity-elon-musk"]
	if elon.sceneID != "scene-elon" || elon.entityID != "entity-elon-musk" {
		t.Fatalf("Elon Musk association ids = %+v, want scene-elon/entity-elon-musk", elon)
	}
	if elon.assetID != "asset-elon-musk" {
		t.Fatalf("entity-elon-musk bound asset_id=%q, want asset-elon-musk", elon.assetID)
	}
	if elon.assetID == "asset-cody-rhodes" {
		t.Fatal("entity-elon-musk bound to Cody Rhodes' asset")
	}

	tesla := got["entity-tesla"]
	if tesla.sceneID != "scene-elon" || tesla.entityID != "entity-tesla" {
		t.Fatalf("Tesla association ids = %+v, want scene-elon/entity-tesla", tesla)
	}
	if tesla.assetID != "asset-tesla" {
		t.Fatalf("entity-tesla bound asset_id=%q, want asset-tesla", tesla.assetID)
	}
}
