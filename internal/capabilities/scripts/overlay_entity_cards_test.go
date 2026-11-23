package scriptgeneration

import (
	"strings"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
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
