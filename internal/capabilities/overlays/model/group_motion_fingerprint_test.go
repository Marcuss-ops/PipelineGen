package model

import "testing"

// The group choreography is the only motion owner of a new composite, so it
// must participate in the render key. Otherwise two different group motions
// over the same assets and windows would reuse the first render.
func TestRenderKeyIncludesGroupMotion(t *testing.T) {
	plan := OverlayPlan{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1}
	item := OverlayItem{
		ID: "group", Kind: "entity_image", TemplateID: "image_popup",
		StartMs: 0, EndMs: 5000, StartUS: 0, DurationUS: 5_000_000,
		GroupMotionID: "duo_split_reveal",
		ImageLayers: []OverlayImageLayer{
			{ID: "a", AssetID: "a", StartMS: 0, EndMS: 5000},
			{ID: "b", AssetID: "b", StartMS: 0, EndMS: 5000},
		},
	}
	first := ComputeRenderKey(plan, item)
	if stable := ComputeRenderKey(plan, item); stable != first {
		t.Fatalf("same group motion changed the render key: %s vs %s", first, stable)
	}
	item.GroupMotionID = "duo_soft_bloom_rise"
	if second := ComputeRenderKey(plan, item); second == first {
		t.Fatal("different group motions share a render key")
	}
}
