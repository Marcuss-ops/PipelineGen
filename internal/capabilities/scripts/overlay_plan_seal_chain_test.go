package scriptgeneration

import (
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func TestPromoteVerifiedEntityCardsInsideMixedChain(t *testing.T) {
	stable := capabilityentities.StableEntityID("PERSON", "Steve Jobs")
	plan := &capmodel.OverlayPlan{
		PlanID: "plan-chain-1",
		Items: []capmodel.OverlayItem{
			{
				ID:         "ov-chain-1",
				SceneID:    "scene-1",
				Kind:       capmodel.KindMixedChain,
				TemplateID: capmodel.TemplateMixedChain,
				Text:       "Steve Jobs",
				Params: map[string]any{
					"chain_segments": []any{
						map[string]any{
							"kind":      string(capmodel.KindEntityCard),
							"text":      "Steve Jobs",
							"entity_id": stable,
						},
						map[string]any{
							"kind":      string(capmodel.KindEntityCard),
							"text":      "Apple",
							"entity_id": "organization:apple",
						},
					},
				},
			},
		},
	}
	result := &GenerateResult{
		SemanticRenderBundle: promotionTestBundle(
			stable, "person:steve-jobs", "Steve Jobs",
			"https://drive.google.com/uc?export=download&id=steve",
		),
	}

	promoteVerifiedEntityCards(plan, result, OverlayCanvasSpec{})

	item := plan.Items[0]
	if item.Kind != capmodel.KindMixedChain {
		t.Fatalf("expected mixed_chain when only 1 of 2 segments promoted, got %q", item.Kind)
	}
	if len(item.AssetRefs) != 1 || item.AssetRefs[0].URL != "https://drive.google.com/uc?export=download&id=steve" {
		t.Fatalf("expected parent AssetRefs to contain promoted asset, got %+v", item.AssetRefs)
	}
	segs, ok := capmodel.ChainSegmentMaps(item.Params)
	if !ok || len(segs) != 2 {
		t.Fatalf("expected 2 chain segments, got %+v", item.Params["chain_segments"])
	}
	if segs[0]["kind"] != string(capmodel.KindEntityImage) {
		t.Fatalf("expected seg[0] kind=entity_image, got %v", segs[0]["kind"])
	}
	if segs[1]["kind"] != string(capmodel.KindEntityCard) {
		t.Fatalf("expected unindexed seg[1] to remain entity_card, got %v", segs[1]["kind"])
	}
}
