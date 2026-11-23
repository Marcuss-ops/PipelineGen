package scriptgeneration

import (
	"strings"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
)

// washingtonSurnameResult reproduces the s4 it/en live shape: the portrait
// belongs to the PERSON "Desiree Washington", and the NER also extracted the
// GPE "Washington" from the same surface. The bundle indexes a PERSON surname
// alias ("washington") so a translated card that keeps only the family name
// still resolves — that alias must never cross into the PLACE family.
func washingtonSurnameResult() *GenerateResult {
	return &GenerateResult{
		SemanticRenderBundle: &capabilityoverlay.SemanticRenderBundleV1{
			Entities: []capabilityoverlay.ResolvedEntity{{
				EntityID: "person-desiree", CanonicalEntityID: "person:desiree-washington",
				Type: "PERSON", Text: "Desiree Washington", CanonicalText: "Desiree Washington",
				SceneID: "scene-1",
			}},
			Assets: []capabilityoverlay.BoundAsset{{
				EntityID: "person-desiree", AssetID: "portrait-desiree",
				ContentHash: strings.Repeat("d", 64),
				SourceURL:   "https://drive.google.com/uc?export=download&id=desiree",
				LocalPath:   "/cache/desiree.jpg", Verified: true,
			}},
		},
		EntityTimeline: &capabilityentities.EntityTimeline{
			Version: 1, Language: "en", DurationUS: 20_000_000,
			Scenes: []capabilityentities.SceneEntityTimeline{
				{
					SceneID: "scene-1", TimelineStartUS: 0,
					Entities: []capabilityentities.EntityOccurrence{{
						EntityID: "person-desiree", Name: "Desiree Washington", Type: "PERSON",
						SceneID: "scene-1", LocalStartUS: 500_000, LocalEndUS: 1_500_000,
						TimelineStartUS: 0, AudioStartUS: 500_000, AudioEndUS: 1_500_000,
						Confidence: 0.95,
					}},
				},
				{
					SceneID: "scene-2", TimelineStartUS: 10_000_000,
					Entities: []capabilityentities.EntityOccurrence{{
						EntityID: "gpe-washington", Name: "Washington", Type: "GPE",
						SceneID: "scene-2", LocalStartUS: 500_000, LocalEndUS: 1_000_000,
						TimelineStartUS: 10_000_000, AudioStartUS: 10_500_000, AudioEndUS: 11_000_000,
						Confidence: 0.9,
					}},
				},
			},
		},
	}
}

// TestResolvedEntityAssetsNeverCrossTheSurnameBetweenFamilies pins the live
// defect: the GPE "Washington" inherited Desiree Washington's portrait, the
// audit rejected the binding as unverified for that entity, and the person card
// was left unplanned. A surname alias is a PERSON projection; a place of the
// same surface has no portrait of its own and must receive none.
func TestResolvedEntityAssetsNeverCrossTheSurnameBetweenFamilies(t *testing.T) {
	assets := resolvedEntityAssets(washingtonSurnameResult(), nil)
	if len(assets) != 1 {
		t.Fatalf("resolved assets = %#v, want only the PERSON portrait", assets)
	}
	if assets[0].EntityID != "person-desiree" {
		t.Fatalf("resolved entity = %q, want the PERSON occurrence", assets[0].EntityID)
	}
	for _, asset := range assets {
		if asset.EntityID == "gpe-washington" {
			t.Fatalf("the GPE inherited a portrait: %#v", asset)
		}
	}
}

// TestSurnameAliasStillResolvesItsOwnFamily keeps the legitimate projection:
// a localized PERSON card that keeps only the family name ("Washington") still
// joins the verified portrait of that same person.
func TestSurnameAliasStillResolvesItsOwnFamily(t *testing.T) {
	result := washingtonSurnameResult()
	result.EntityTimeline.Scenes[1].Entities[0].Type = "PERSON"
	assets := resolvedEntityAssets(result, nil)
	var localized *overlayplan.VerifiedEntityAsset
	for i := range assets {
		if assets[i].EntityID == "gpe-washington" {
			localized = &assets[i]
		}
	}
	if localized == nil {
		t.Fatalf("resolved assets = %#v, want the surname alias to resolve", assets)
	}
	if localized.CanonicalEntityID != "person:desiree-washington" {
		t.Fatalf("canonical id = %q, want the same PERSON identity", localized.CanonicalEntityID)
	}
}

func TestBundleNameKeyIsTyped(t *testing.T) {
	if got, want := bundleNameKey("GPE", "Washington"), "bundle-name:PLACE:washington"; got != want {
		t.Fatalf("place key = %q, want %q", got, want)
	}
	if got, want := bundleNameKey("PERSON", "Washington"), "bundle-name:PERSON:washington"; got != want {
		t.Fatalf("person key = %q, want %q", got, want)
	}
	if got := bundleNameKey("PERSON", "  "); got != "" {
		t.Fatalf("empty surface key = %q, want none", got)
	}
}
