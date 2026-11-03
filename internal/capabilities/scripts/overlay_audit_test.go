package scriptgeneration

import (
	"strings"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestBuildOverlayAuditExposesImageBindingsAndStableDigest(t *testing.T) {
	bundle := &capabilityoverlay.SemanticRenderBundleV1{
		Entities: []capabilityoverlay.ResolvedEntity{
			{EntityID: "person:ada", Type: "PERSON", Text: "Ada", CanonicalText: "Ada Lovelace", SceneID: "scene-1", Start: 0, End: 3},
			{EntityID: "person:grace", Type: "PERSON", Text: "Grace", CanonicalText: "Grace Hopper", SceneID: "scene-1", Start: 4, End: 9},
		},
		Assets: []capabilityoverlay.BoundAsset{{
			EntityID: "person:ada", AssetID: "asset-ada", ContentHash: strings.Repeat("a", 64), Verified: true,
		}},
	}
	plan := &capabilityoverlay.OverlayPlan{
		PlanID:  "plan-audit",
		VideoID: "video-audit",
		Items: []capabilityoverlay.OverlayItem{
			{
				ID: "overlay-ada", SceneID: "scene-1", Kind: "entity_image",
				EntityID: "entity-ada",
				EntityRef: &capabilityoverlay.OverlayEntityRef{
					EntityID: "item-ada", CanonicalEntityID: "person:ada",
					Type: "PERSON", Name: "Ada Lovelace", SurfaceText: "Ada",
				},
				AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "asset-ada"}},
			},
			{
				ID: "overlay-grace", SceneID: "scene-1", Kind: "entity_card",
				EntityID: "entity-grace",
				EntityRef: &capabilityoverlay.OverlayEntityRef{
					EntityID: "item-grace", CanonicalEntityID: "person:grace",
					Type: "PERSON", Name: "Grace Hopper", SurfaceText: "Grace",
				},
				Text: "Grace",
			},
		},
	}

	audit := buildOverlayAuditWithLineage(bundle, plan, nil)
	if audit == nil {
		t.Fatal("audit is nil")
	}
	if audit.PlanSHA256 == "" {
		t.Fatal("plan digest is empty")
	}
	second := buildOverlayAuditWithLineage(bundle, plan, nil)
	if second.PlanSHA256 != audit.PlanSHA256 {
		t.Fatalf("plan digest is not deterministic: %q != %q", second.PlanSHA256, audit.PlanSHA256)
	}
	if audit.PlanID != "plan-audit" || audit.VideoID != "video-audit" {
		t.Fatalf("plan identity = %q/%q", audit.PlanID, audit.VideoID)
	}
	if len(audit.ImageBindings) != 1 {
		t.Fatalf("image bindings = %d, want 1: %+v", len(audit.ImageBindings), audit.ImageBindings)
	}
	binding := audit.ImageBindings[0]
	if binding.EntityID != "person:ada" || binding.OverlayID != "overlay-ada" {
		t.Fatalf("image binding identity = %+v", binding)
	}
	if len(binding.AssetIDs) != 1 || binding.AssetIDs[0] != "asset-ada" || !binding.HasVerified {
		t.Fatalf("image binding assets = %+v, want verified asset-ada", binding)
	}

	var ada, grace *OverlayAuditEntity
	for i := range audit.DetectedEntities {
		switch audit.DetectedEntities[i].ID {
		case "person:ada":
			ada = &audit.DetectedEntities[i]
		case "person:grace":
			grace = &audit.DetectedEntities[i]
		}
	}
	if ada == nil || grace == nil {
		t.Fatalf("detected entities = %+v, want Ada and Grace", audit.DetectedEntities)
	}
	if ada.ImageStatus != "planned" || !ada.HasVerifiedImage || len(ada.ImageAssetIDs) != 1 || ada.ImageAssetIDs[0] != "asset-ada" {
		t.Fatalf("Ada image audit = %+v, want planned verified asset", ada)
	}
	if len(ada.PlannedKinds) != 1 || ada.PlannedKinds[0] != "entity_image" {
		t.Fatalf("Ada planned kinds = %v, want entity_image", ada.PlannedKinds)
	}
	if grace.ImageStatus != "missing" || !grace.SelectedForVideo || len(grace.ImageAssetIDs) != 0 {
		t.Fatalf("Grace image audit = %+v, want selected text-only with no image", grace)
	}

	plan.Items[0].ID = "overlay-ada-renamed"
	changed := buildOverlayAuditWithLineage(bundle, plan, nil)
	if changed.PlanSHA256 == audit.PlanSHA256 {
		t.Fatal("plan digest did not change when the plan content changed")
	}
}
