package scriptgeneration

import (
	"strings"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// promotionSurfaceResult is one PERSON occurrence whose portrait exists only in
// the verified bundle: the exact shape the Go media plane used to promote after
// the seal, now shipped to the planner.
func promotionSurfaceResult() *GenerateResult {
	return &GenerateResult{
		SemanticRenderBundle: &capabilityoverlay.SemanticRenderBundleV1{
			Entities: []capabilityoverlay.ResolvedEntity{{
				EntityID: "person-ada", CanonicalEntityID: "person:ada-lovelace",
				Type: "PERSON", Text: "Ada Lovelace", CanonicalText: "Ada Lovelace",
				SceneID: "scene-1",
			}},
			Assets: []capabilityoverlay.BoundAsset{{
				EntityID: "person-ada", AssetID: "portrait-ada",
				ContentHash: strings.Repeat("1", 64),
				SourceURL:   "https://drive.google.com/uc?export=download&id=ada",
				LocalPath:   "/cache/ada.jpg", Verified: true,
			}},
		},
		EntityTimeline: &capabilityentities.EntityTimeline{
			Version: 1, Language: "it", DurationUS: 8_000_000,
			Scenes: []capabilityentities.SceneEntityTimeline{{
				SceneID: "scene-1", TimelineStartUS: 0,
				Entities: []capabilityentities.EntityOccurrence{{
					EntityID: "person-ada", Name: "Ada Lovelace", Type: "PERSON",
					SceneID: "scene-1", LocalStartUS: 500_000, LocalEndUS: 1_500_000,
					TimelineStartUS: 0, AudioStartUS: 500_000, AudioEndUS: 1_500_000,
					Confidence: 0.95,
				}},
			}},
		},
	}
}

func TestPromotableEntityAssetsCarryThePlanIdentityAndTheVerifiedBytes(t *testing.T) {
	assets := promotableEntityAssets(promotionSurfaceResult())
	if len(assets) != 1 {
		t.Fatalf("promotable assets = %d, want one entry for the occurrence", len(assets))
	}
	got := assets[0]
	if got.EntityID != "person-ada" {
		t.Fatalf("entity id = %q, want the plan occurrence identity", got.EntityID)
	}
	if got.CanonicalEntityID != "person:ada-lovelace" {
		t.Fatalf("canonical id = %q, want the resolver identity", got.CanonicalEntityID)
	}
	if len(got.Assets) != 1 {
		t.Fatalf("assets = %d, want the resolved portrait", len(got.Assets))
	}
	asset := got.Assets[0]
	if asset.AssetID != "portrait-ada" || asset.SHA256 != strings.Repeat("1", 64) || asset.URL == "" {
		t.Fatalf("promotable asset = %#v, want the content-addressed portrait", asset)
	}
}

func TestPromotableEntityAssetsSkipUnrenderableRecords(t *testing.T) {
	for name, mutate := range map[string]func(*capabilityoverlay.SemanticRenderBundleV1){
		"no content address": func(bundle *capabilityoverlay.SemanticRenderBundleV1) {
			bundle.Assets[0].ContentHash = ""
		},
		"placeholder url": func(bundle *capabilityoverlay.SemanticRenderBundleV1) {
			bundle.Assets[0].SourceURL = "assets/semantic/person-ada"
		},
		"unverified": func(bundle *capabilityoverlay.SemanticRenderBundleV1) {
			bundle.Assets[0].Verified = false
		},
	} {
		result := promotionSurfaceResult()
		mutate(result.SemanticRenderBundle)
		if assets := promotableEntityAssets(result); len(assets) != 0 {
			t.Fatalf("%s: promotable assets = %#v, want none", name, assets)
		}
	}
}

func TestPromotableEntityAssetsOnlyCoverPlanOccurrences(t *testing.T) {
	result := promotionSurfaceResult()
	result.EntityTimeline = nil
	if assets := promotableEntityAssets(result); len(assets) != 0 {
		t.Fatalf("promotable assets without a timeline = %#v, want none", assets)
	}
}

func TestV2CanvasCarriesTheCertifiedImagePresetCatalog(t *testing.T) {
	canvas, err := overlayPlanCanvas(OverlayCanvasSpec{})
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	want := capabilityoverlay.ImagePresetCandidates()
	if len(want) == 0 {
		t.Fatal("the registry returned no preset.image candidates")
	}
	if strings.Join(canvas.ImagePresets, ",") != strings.Join(want, ",") {
		t.Fatalf("image_presets = %v, want the preset.image vocabulary %v", canvas.ImagePresets, want)
	}
}

func TestPromotableLocalPathsReattachByContentAddress(t *testing.T) {
	paths := promotableLocalPaths(promotionSurfaceResult())
	got, ok := paths[strings.Repeat("1", 64)]
	if !ok {
		t.Fatalf("local paths = %#v, want the bundle portrait by content address", paths)
	}
	if got.LocalPath != "/cache/ada.jpg" {
		t.Fatalf("local path = %q, want the materialized cache location", got.LocalPath)
	}
}
