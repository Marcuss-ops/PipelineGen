package scriptgeneration

import (
	"strings"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func promotionTestBundle(entityID, canonicalID, text, url string) *capabilityoverlay.SemanticRenderBundleV1 {
	return &capabilityoverlay.SemanticRenderBundleV1{
		Entities: []capabilityoverlay.ResolvedEntity{{
			EntityID: entityID, CanonicalEntityID: canonicalID, Type: "PERSON",
			Text: text, CanonicalText: text, SceneID: "scene-1",
		}},
		Assets: []capabilityoverlay.BoundAsset{{
			EntityID: entityID, AssetID: "asset-" + strings.ToLower(canonicalID),
			ContentHash: strings.Repeat("a", 64), SourceURL: url, Verified: true,
		}},
	}
}

func TestPromoteVerifiedEntityCardsUpgradesCompiledCard(t *testing.T) {
	stable := capabilityentities.StableEntityID("PERSON", "Francis Kaufmann")
	result := &GenerateResult{SemanticRenderBundle: promotionTestBundle(
		stable, "person:francis-kaufmann", "Francis Kaufmann",
		"https://drive.google.com/uc?export=download&id=francis",
	)}
	plan := &capmodel.OverlayPlan{PlanID: "plan-1", Items: []capmodel.OverlayItem{
		{
			ID: "card-francis", SceneID: "scene-1", EntityID: stable,
			Kind: string(capmodel.KindEntityCard), TemplateID: "person_default",
			EntityRef: &capmodel.OverlayEntityRef{EntityID: stable, Type: "PERSON", Name: "Francis Kaufmann"},
			StartMs:   0, EndMs: 5000,
		},
		{ID: "phrase", SceneID: "scene-1", Kind: "text_phrase", Text: "a phrase"},
	}}

	promoteVerifiedEntityCards(plan, result, OverlayCanvasSpec{})

	if got := plan.Items[0]; got.Kind != string(capmodel.KindEntityImage) || len(got.AssetRefs) != 1 {
		t.Fatalf("entity card = %#v, want promoted entity_image with one verified asset", got)
	}
	if got := plan.Items[0].AssetRefs[0]; got.SHA256 != strings.Repeat("a", 64) || got.URL == "" {
		t.Fatalf("promoted asset ref = %#v, want content address and renderable URL", got)
	}
	if plan.Items[1].Kind != "text_phrase" {
		t.Fatalf("non-entity item kind = %q, want untouched text_phrase", plan.Items[1].Kind)
	}
}

func TestPromoteVerifiedEntityCardsReusesSourcePortraitForLocalizedCard(t *testing.T) {
	const sha = "1a2a150dfe9f0000000000000000000000000000000000000000000000000000aa"
	sourceImage := &scriptpkg.EntityImageBinding{
		Status: "resolved", AssetID: "entity-image-mike-tyson", SHA256: sha,
		PreviewURL: "https://drive.google.com/uc?export=download&id=entity-image-mike-tyson",
		MediaType:  "image/jpeg",
	}
	result := &GenerateResult{Scenes: []Scene{{
		ID: "scene-1",
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			Type: "PERSON", CanonicalName: "Mike Tyson", CanonicalEntityID: "person:mike-tyson", Image: sourceImage,
		}}},
		LocalizedAnnotations: map[Language]*scriptpkg.SceneAnnotations{
			"pl": {PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				Type: "PERSON", CanonicalName: "Mike’a Tysona", CanonicalEntityID: "person:mike-tyson",
			}}},
		},
	}}}
	localizedStable := capabilityentities.StableEntityID("PERSON", "Mike’a Tysona")
	plan := &capmodel.OverlayPlan{PlanID: "plan-pl", Items: []capmodel.OverlayItem{{
		ID: "card-mike-pl", SceneID: "scene-1", EntityID: localizedStable,
		Kind: string(capmodel.KindEntityCard), TemplateID: "person_default",
		EntityRef: &capmodel.OverlayEntityRef{Type: "PERSON", Name: "Mike’a Tysona", CanonicalEntityID: "person:mike-tyson"},
	}}}

	promoteVerifiedEntityCards(plan, result, OverlayCanvasSpec{})

	got := plan.Items[0]
	if got.Kind != string(capmodel.KindEntityImage) || len(got.AssetRefs) != 1 {
		t.Fatalf("localized card = %#v, want the source portrait reused as entity_image", got)
	}
	if got.AssetRefs[0].SHA256 != sha {
		t.Fatalf("localized asset sha = %q, want the verified source sha %q", got.AssetRefs[0].SHA256, sha)
	}
	if got.EntityRef == nil || got.EntityRef.CanonicalEntityID != "person:mike-tyson" {
		t.Fatalf("localized entity ref = %#v, want the canonical source identity", got.EntityRef)
	}
}

func TestPromoteVerifiedEntityCardsHonorsDisabledImages(t *testing.T) {
	stable := capabilityentities.StableEntityID("PERSON", "Francis Kaufmann")
	result := &GenerateResult{SemanticRenderBundle: promotionTestBundle(
		stable, "person:francis-kaufmann", "Francis Kaufmann",
		"https://drive.google.com/uc?export=download&id=francis",
	)}
	plan := &capmodel.OverlayPlan{PlanID: "plan-1", Items: []capmodel.OverlayItem{{
		ID: "card-francis", SceneID: "scene-1", EntityID: stable,
		Kind: string(capmodel.KindEntityCard), TemplateID: "person_default",
		EntityRef: &capmodel.OverlayEntityRef{EntityID: stable, Type: "PERSON", Name: "Francis Kaufmann"},
	}}}
	canvas := OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryImages: scriptpkg.ToggleDisabled,
	}}

	promoteVerifiedEntityCards(plan, result, canvas)

	if got := plan.Items[0].Kind; got != string(capmodel.KindEntityCard) {
		t.Fatalf("entity card kind = %q, want text-only entity_card when images are explicitly disabled", got)
	}
}

func TestPromoteVerifiedEntityCardsLeavesUnverifiedCardTextOnly(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{{
		ID: "scene-1",
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			Type: "PERSON", CanonicalName: "Andrea Sempio", CanonicalEntityID: "person:andrea-sempio",
			Image: &scriptpkg.EntityImageBinding{Status: "resolved", AssetID: "unhashed", PreviewURL: "https://example.test/a.jpg"},
		}}},
	}}}
	plan := &capmodel.OverlayPlan{PlanID: "plan-1", Items: []capmodel.OverlayItem{{
		ID: "card-andrea", SceneID: "scene-1",
		Kind: string(capmodel.KindEntityCard), TemplateID: "person_default",
		EntityRef: &capmodel.OverlayEntityRef{Type: "PERSON", Name: "Andrea Sempio", CanonicalEntityID: "person:andrea-sempio"},
	}}}

	promoteVerifiedEntityCards(plan, result, OverlayCanvasSpec{})

	if got := plan.Items[0].Kind; got != string(capmodel.KindEntityCard) {
		t.Fatalf("entity card kind = %q, want text-only card without a verified content address", got)
	}
}
