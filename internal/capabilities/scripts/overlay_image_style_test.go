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

// TestApplyImageAssetStylesRejectsUnhostablePin fails closed when a scene has
// no image item for the pin instead of silently dropping the style.
func TestApplyImageAssetStylesRejectsUnhostablePin(t *testing.T) {
	plan := styleTestPlan()
	pins := []scriptpkg.OverlayImageAsset{{SceneID: "scene-3", Style: &scriptpkg.OverlayImageStyleSpec{Radius: styleTestFloat(8)}}}
	err := applyImageAssetStyles([]*capmodel.OverlayPlan{plan}, pins, nil)
	if err == nil {
		t.Fatal("expected an error for a pin whose scene has no image item")
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
