package scriptgeneration

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func styleTestPlan() *capmodel.OverlayPlan {
	return &capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan, PlanID: "pin-style", VideoID: "video",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationMS: 10_000,
		Items: []capmodel.OverlayItem{
			{ID: "s1-first", SceneID: "scene-1", Kind: "image", TemplateID: "IMAGE_OVERLAY",
				StartMs: 1000, EndMs: 4000, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "a"}}},
			{ID: "s1-second", SceneID: "scene-1", Kind: "image", TemplateID: "IMAGE_OVERLAY",
				StartMs: 2000, EndMs: 5000, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "b"}}},
			{ID: "s2-only", SceneID: "scene-2", Kind: "image", TemplateID: "IMAGE_OVERLAY",
				StartMs: 1000, EndMs: 4000, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "c"}}},
		},
	}
}

// TestApplyImageAssetStylesBindsPerScene pins the positional match: the pin
// with a grid override decorates the FIRST image of its scene by start time,
// the second image of the same scene is left alone, and another scene's pin
// carries its own effect stack.
func TestApplyImageAssetStylesBindsPerScene(t *testing.T) {
	plan := styleTestPlan()
	runStyle := &scriptpkg.OverlayImageStyleSpec{
		Grid: &scriptpkg.OverlayImageGridSpec{Enabled: styleTestBool(true), Color: "#112233"},
	}
	pins := []scriptpkg.OverlayImageAsset{
		{SceneID: "scene-1", Style: &scriptpkg.OverlayImageStyleSpec{Grid: &scriptpkg.OverlayImageGridSpec{Enabled: styleTestBool(false)}}},
		{SceneID: "scene-2", Style: &scriptpkg.OverlayImageStyleSpec{Effects: []scriptpkg.ImageEffectSpec{{Type: "vignette", Amount: 0.4}}}},
	}
	if err := applyImageAssetStyles([]*capmodel.OverlayPlan{plan}, pins, runStyle); err != nil {
		t.Fatalf("apply pin styles: %v", err)
	}
	first := plan.Items[0]
	grid, ok := first.Params["grid"].(map[string]any)
	if !ok {
		t.Fatalf("first image of scene-1 got no grid override: %+v", first.Params)
	}
	if enabled, ok := grid["enabled"].(bool); !ok || enabled {
		t.Fatalf("grid.enabled=false was not projected: %+v", grid)
	}
	if grid["color"] != "#112233" {
		t.Fatalf("pin grid must keep the run-level colour: %+v", grid)
	}
	if _, decorated := plan.Items[1].Params["grid"]; decorated {
		t.Fatalf("second image of scene-1 must stay untouched by the first pin: %+v", plan.Items[1].Params)
	}
	effects, ok := plan.Items[2].Params["effects"].([]map[string]any)
	if !ok || len(effects) != 1 || effects[0]["type"] != "vignette" {
		t.Fatalf("scene-2 pin effects were not projected: %+v", plan.Items[2].Params["effects"])
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("sealed plan after a pin style write is invalid: %v", err)
	}
}

// TestApplyImageAssetStylesIgnoresEntityCards pins the positional contract: an
// entity card is an image surface but not a still, so it must not consume a
// pin slot. Otherwise the still after the portrait keeps the planner's default
// 1340x760 box (r6) and the pin lands on the wrong surface.
func TestApplyImageAssetStylesIgnoresEntityCards(t *testing.T) {
	plan := styleTestPlan()
	portrait := capmodel.OverlayItem{ID: "s1-portrait", SceneID: "scene-1", Kind: string(capmodel.KindEntityImage),
		TemplateID: "image_popup",
		StartMs:    1500, EndMs: 2500, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "p"}}}
	plan.Items = append(plan.Items, portrait)
	height := 700
	fit := "cover"
	pins := []scriptpkg.OverlayImageAsset{
		{SceneID: "scene-1", Style: &scriptpkg.OverlayImageStyleSpec{Height: &height, Fit: fit}},
		{SceneID: "scene-1", Style: &scriptpkg.OverlayImageStyleSpec{Height: &height, Fit: fit}},
	}
	if err := applyImageAssetStyles([]*capmodel.OverlayPlan{plan}, pins, nil); err != nil {
		t.Fatalf("apply pin styles: %v", err)
	}
	for _, item := range plan.Items {
		if item.Kind == string(capmodel.KindEntityImage) {
			if len(item.Params) != 0 {
				t.Fatalf("entity card was styled by a still pin: %+v", item.Params)
			}
			continue
		}
		// Both pins target scene-1: the portrait must not consume one of
		// them, so the two stills of that scene are the ones that receive
		// the height. A still in another scene is untouched.
		if item.SceneID == "scene-1" {
			if item.Params["height"] != 700 {
				t.Fatalf("still %q did not receive its pin: %+v", item.ID, item.Params)
			}
			continue
		}
		if len(item.Params) != 0 {
			t.Fatalf("still %q was decorated by a pin for another scene: %+v", item.ID, item.Params)
		}
	}
}

// TestApplyImageAssetStylesSkipsPinForSceneWithoutImage pins the optional-still
// contract (r134): a scene may have no standalone image item because the
// provider returned no verified still or the planner folded it into an image
// chain. A pin for that scene is skipped, never a whole-language failure; the
// missing still stays visible in the overlay audit.
func TestApplyImageAssetStylesSkipsPinForSceneWithoutImage(t *testing.T) {
	plan := styleTestPlan()
	pins := []scriptpkg.OverlayImageAsset{{SceneID: "scene-3", Style: &scriptpkg.OverlayImageStyleSpec{Radius: styleTestFloat(8)}}}
	if err := applyImageAssetStyles([]*capmodel.OverlayPlan{plan}, pins, nil); err != nil {
		t.Fatalf("a pin for a scene without a standalone still must be skipped: %v", err)
	}
	for _, item := range plan.Items {
		if len(item.Params) != 0 || item.Frame != nil {
			t.Fatalf("item %q was decorated by a pin that has no target: %+v %+v", item.ID, item.Params, item.Frame)
		}
	}
}

// TestPayloadImageStyleAppliesWithoutPins pins the seal-time projection: a
// run-level overlay_style.image reaches image items even when the payload
// declares no overlay_images pins (applyImageAssetStyles returns early on an
// empty scene list, so it cannot be the only owner of the run-level look).
func TestPayloadImageStyleAppliesWithoutPins(t *testing.T) {
	plan := styleTestPlan()
	radius := 18.0
	items := append(plan.Items, capmodel.OverlayItem{ID: "phrase", Kind: "text_phrase", Params: map[string]any{}})
	applyPayloadOverlayStyleToItems(items, OverlayCanvasSpec{Style: &scriptpkg.OverlayStyleSpec{
		Image: &scriptpkg.OverlayImageStyleSpec{Radius: &radius},
	}})
	for index := 0; index < 3; index++ {
		if got := items[index].Params["radius"]; got != 18.0 {
			t.Fatalf("image %d radius = %#v, want 18", index, got)
		}
		if items[index].Frame == nil {
			t.Fatalf("image %d has no frame after the run-level style projection", index)
		}
	}
	if _, decorated := items[3].Params["radius"]; decorated {
		t.Fatalf("text item received the image style: %+v", items[3].Params)
	}
}

// TestApplyImageAssetStylesTruncatesPinsBeyondPlannedStills keeps the
// positional match well-defined when a scene declares more pins than the plan
// has images: the first pins bind in start-time order and the extras have no
// target, so the run continues instead of failing on a cosmetic pin.
func TestApplyImageAssetStylesTruncatesPinsBeyondPlannedStills(t *testing.T) {
	plan := styleTestPlan()
	pins := []scriptpkg.OverlayImageAsset{
		{SceneID: "scene-2", Style: &scriptpkg.OverlayImageStyleSpec{Grid: &scriptpkg.OverlayImageGridSpec{Enabled: styleTestBool(false)}}},
		{SceneID: "scene-2", Style: &scriptpkg.OverlayImageStyleSpec{Grid: &scriptpkg.OverlayImageGridSpec{Enabled: styleTestBool(true)}}},
	}
	if err := applyImageAssetStyles([]*capmodel.OverlayPlan{plan}, pins, nil); err != nil {
		t.Fatalf("pins beyond the planned stills must be truncated, not fail the run: %v", err)
	}
	grid, ok := plan.Items[2].Params["grid"].(map[string]any)
	if !ok {
		t.Fatalf("the first pin of scene-2 was not applied: %+v", plan.Items[2].Params)
	}
	if enabled, ok := grid["enabled"].(bool); !ok || enabled {
		t.Fatalf("the first pin of scene-2 must win, got grid=%+v", grid)
	}
}

// TestApplyImageAssetStylesNoopWithoutPins keeps the untouched plan untouched.
func TestApplyImageAssetStylesNoopWithoutPins(t *testing.T) {
	plan := styleTestPlan()
	if err := applyImageAssetStyles([]*capmodel.OverlayPlan{plan}, nil, nil); err != nil {
		t.Fatalf("apply without pins: %v", err)
	}
	for _, item := range plan.Items {
		if len(item.Params) != 0 || item.Frame != nil {
			t.Fatalf("item %q was decorated without a pin: %+v %+v", item.ID, item.Params, item.Frame)
		}
	}
}

func styleTestBool(value bool) *bool        { return &value }
func styleTestFloat(value float64) *float64 { return &value }
