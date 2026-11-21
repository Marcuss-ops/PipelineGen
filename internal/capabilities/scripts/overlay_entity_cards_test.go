package scriptgeneration

import (
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	generationcontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/generation"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestEntityCardMediaIndexUsesSemanticBundleAssets(t *testing.T) {
	stableID := capabilityentities.StableEntityID("PERSON", "Francis Kaufmann")
	bundle := &capabilityoverlay.SemanticRenderBundleV1{
		Entities: []capabilityoverlay.ResolvedEntity{{
			EntityID: stableID, CanonicalEntityID: "person:francis-kaufmann", Type: "PERSON",
			Text: "Francis Kaufmann", CanonicalText: "Francis Kaufmann", SceneID: "scene-1",
		}},
		Assets: []capabilityoverlay.BoundAsset{{
			EntityID: stableID, AssetID: "asset-francis", ContentHash: strings.Repeat("b", 64),
			SourceURL: "https://drive.google.com/uc?export=download&id=francis", Verified: true,
		}},
	}
	result := &GenerateResult{SemanticRenderBundle: bundle}
	media, canonicalByStable := entityCardMediaIndex(result)
	lookupID := canonicalByStable[stableID]
	if lookupID != "person:francis-kaufmann" {
		t.Fatalf("lookup id = %q, want the bundle canonical identity", lookupID)
	}
	ref, err := media.ResolveBest(lookupID)
	if err != nil {
		t.Fatalf("resolve bundle portrait: %v", err)
	}
	if ref.URL != "https://drive.google.com/uc?export=download&id=francis" {
		t.Fatalf("bundle-only asset URL = %q, want the verified bundle URL", ref.URL)
	}
	if ref.SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("bundle asset sha = %q, want the verified content address", ref.SHA256)
	}
}

func TestEntityCardMediaIndexUsesSemanticBundleAssetsPerScene(t *testing.T) {
	stableID := capabilityentities.StableEntityID("PERSON", "Francis Kaufmann")
	bundle := &capabilityoverlay.SemanticRenderBundleV1{
		Entities: []capabilityoverlay.ResolvedEntity{{
			EntityID: stableID, CanonicalEntityID: "person:francis-kaufmann", Type: "PERSON",
			Text: "Francis Kaufmann", CanonicalText: "Francis Kaufmann", SceneID: "scene-1",
		}},
		Assets: []capabilityoverlay.BoundAsset{{
			EntityID: stableID, AssetID: "asset-francis", ContentHash: strings.Repeat("c", 64),
			SourceURL: "https://drive.google.com/uc?export=download&id=francis", Verified: true,
		}},
	}
	result := &GenerateResult{SemanticRenderBundle: bundle}
	media, canonicalByStable := entityCardMediaIndex(result, overlayPlanImageOptions{PerSceneEntityCards: true})
	scoped := canonicalByStable[stableID]
	if !strings.Contains(scoped, "::person:francis-kaufmann") {
		t.Fatalf("per-scene lookup id = %q, want a scene-scoped bundle identity", scoped)
	}
	ref, err := media.ResolveBest(scoped)
	if err != nil || ref.SHA256 != strings.Repeat("c", 64) {
		t.Fatalf("per-scene bundle asset = %#v (err %v), want the verified portrait", ref, err)
	}
}

func TestPersonEntityCardReachesPlanFromAdmittedIdentity(t *testing.T) {
	stableID := capabilityentities.StableEntityID("PERSON", "Francis Kaufmann")
	bundle := &capabilityoverlay.SemanticRenderBundleV1{
		Entities: []capabilityoverlay.ResolvedEntity{{
			EntityID: stableID, CanonicalEntityID: "person:francis-kaufmann", Type: "PERSON",
			Text: "Francis Kaufmann", CanonicalText: "Francis Kaufmann", SceneID: "scene-1",
		}},
		Assets: []capabilityoverlay.BoundAsset{{
			EntityID: stableID, AssetID: "asset-francis", ContentHash: strings.Repeat("e", 64),
			SourceURL: "https://drive.google.com/uc?export=download&id=francis", Verified: true,
		}},
	}
	result := &GenerateResult{
		SemanticRenderBundle: bundle,
		SemanticProductionPlan: &SemanticProductionPlan{Identity: []generationcontract.SemanticPlannedIdentity{{
			ID: stableID, Surface: "Francis Kaufmann", Kind: "PERSON", Media: true,
		}}},
	}
	card := capmodel.OverlayItem{
		ID: "overlay-scene-1-francis-kaufmann", SceneID: "scene-1", EntityID: stableID,
		Kind: string(capmodel.KindEntityImage), TemplateID: "image_popup", EntityCaption: "Francis Kaufmann",
		EntityRef: &capmodel.OverlayEntityRef{EntityID: stableID, Type: "PERSON", Name: "Francis Kaufmann",
			CanonicalEntityID: "person:francis-kaufmann"},
		AssetRefs: []capmodel.OverlayAssetRef{{
			AssetID: "asset-francis", URL: "https://drive.google.com/uc?export=download&id=francis",
			SHA256: strings.Repeat("e", 64),
		}},
	}
	got := admitIdentityImagesByProductionPlan([]capmodel.OverlayItem{card}, result.SemanticProductionPlan)
	if len(got) != 1 || got[0].Kind != string(capmodel.KindEntityImage) || len(got[0].AssetRefs) != 1 {
		t.Fatalf("admitted person card = %#v, want an image card with its bundle asset", got)
	}
	if got[0].EntityRef == nil || got[0].EntityRef.CanonicalEntityID != "person:francis-kaufmann" {
		t.Fatalf("admitted person identity = %#v, want canonical entity id", got[0].EntityRef)
	}
}

func TestApplyHeavySentenceBoostPilotsPhrasePriority(t *testing.T) {
	in := &capabilityoverlay.SceneInput{ID: "s1", Phrases: []capabilityoverlay.TimedAnnotation{
		{Text: "il raccolto è crollato", Score: 0.5},
		{Text: "una frase qualunque", Score: 0.5},
	}}
	heavies := []scriptpkg.ImportantSentence{
		{Index: 0, Text: "Il raccolto è crollato a causa della siccità estiva.", Importance: 0.95},
	}
	applyHeavySentenceBoost(in, heavies)
	if in.Phrases[0].Score != 0.95 {
		t.Fatalf("matching phrase must inherit heavy importance, got %v", in.Phrases[0].Score)
	}
	if in.Phrases[1].Score != 0.5 {
		t.Fatalf("non-matching phrase must keep its score, got %v", in.Phrases[1].Score)
	}
}

func TestApplyHeavySentenceBoostNeverLowersAndToleratesEmpty(t *testing.T) {
	in := &capabilityoverlay.SceneInput{ID: "s1", Phrases: []capabilityoverlay.TimedAnnotation{
		{Text: "il raccolto è crollato", Score: 0.98},
	}}
	applyHeavySentenceBoost(in, []scriptpkg.ImportantSentence{
		{Index: 0, Text: "Il raccolto è crollato.", Importance: 0.4},
	})
	if in.Phrases[0].Score != 0.98 {
		t.Fatalf("scores must only rise, got %v", in.Phrases[0].Score)
	}
	applyHeavySentenceBoost(nil, nil)
	applyHeavySentenceBoost(&capabilityoverlay.SceneInput{}, nil)
}
