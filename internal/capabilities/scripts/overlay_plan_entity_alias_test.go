package scriptgeneration

import (
	"strings"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func aliasTestBundle(entries []capabilityoverlay.ResolvedEntity, urls map[string]string) *capabilityoverlay.SemanticRenderBundleV1 {
	bundle := &capabilityoverlay.SemanticRenderBundleV1{Entities: entries}
	for _, entity := range entries {
		bundle.Assets = append(bundle.Assets, capabilityoverlay.BoundAsset{
			EntityID: entity.EntityID, AssetID: "asset-" + entity.EntityID,
			ContentHash: strings.Repeat("f", 64), SourceURL: urls[entity.EntityID], Verified: true,
		})
	}
	return bundle
}

func TestPromoteVerifiedEntityCardsResolvesLocalizedSurnameAlias(t *testing.T) {
	bundle := aliasTestBundle(
		[]capabilityoverlay.ResolvedEntity{{
			EntityID: "person-mike-tyson", CanonicalEntityID: "person:mike-tyson", Type: "PERSON",
			Text: "Mike Tyson", CanonicalText: "Mike Tyson", SceneID: "scene-03",
		}},
		map[string]string{"person-mike-tyson": "https://drive.google.com/uc?export=download&id=entity-image-72152"},
	)
	result := &GenerateResult{SemanticRenderBundle: bundle}
	plan := &capmodel.OverlayPlan{PlanID: "plan-pl", Items: []capmodel.OverlayItem{{
		ID: "static__tyson__concept__01", SceneID: "scene-03",
		Kind: string(capmodel.KindConcept), TemplateID: "concept_default", Text: "Tyson",
		EntityRef: &capmodel.OverlayEntityRef{Type: "CONCEPT", Name: "Tyson"},
	}}}

	promoteVerifiedEntityCards(plan, result, OverlayCanvasSpec{})

	got := plan.Items[0]
	if got.Kind != string(capmodel.KindEntityImage) || len(got.AssetRefs) != 1 {
		t.Fatalf("localized alias card = %#v, want the verified source portrait reused", got)
	}
	if got.AssetRefs[0].SHA256 != strings.Repeat("f", 64) {
		t.Fatalf("alias asset sha = %q, want the bundle content address", got.AssetRefs[0].SHA256)
	}
	if got.EntityRef == nil || got.EntityRef.CanonicalEntityID != "person:mike-tyson" {
		t.Fatalf("alias entity ref = %#v, want the canonical source identity", got.EntityRef)
	}
}

func TestPromoteVerifiedEntityCardsSkipsAmbiguousSurnameAlias(t *testing.T) {
	bundle := aliasTestBundle(
		[]capabilityoverlay.ResolvedEntity{
			{EntityID: "person-mike-tyson", CanonicalEntityID: "person:mike-tyson", Type: "PERSON", Text: "Mike Tyson", CanonicalText: "Mike Tyson"},
			{EntityID: "person-john-tyson", CanonicalEntityID: "person:john-tyson", Type: "PERSON", Text: "John Tyson", CanonicalText: "John Tyson"},
		},
		map[string]string{
			"person-mike-tyson": "https://drive.google.com/uc?export=download&id=mike",
			"person-john-tyson": "https://drive.google.com/uc?export=download&id=john",
		},
	)
	result := &GenerateResult{SemanticRenderBundle: bundle}
	plan := &capmodel.OverlayPlan{PlanID: "plan-pl", Items: []capmodel.OverlayItem{{
		ID: "static__tyson__concept__01", SceneID: "scene-03",
		Kind: string(capmodel.KindConcept), TemplateID: "concept_default", Text: "Tyson",
		EntityRef: &capmodel.OverlayEntityRef{Type: "CONCEPT", Name: "Tyson"},
	}}}

	promoteVerifiedEntityCards(plan, result, OverlayCanvasSpec{})

	if got := plan.Items[0].Kind; got != string(capmodel.KindConcept) {
		t.Fatalf("ambiguous surname card kind = %q, want text-only concept: a shared family name must not be guessed", got)
	}
}
