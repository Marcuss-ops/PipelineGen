package scriptgeneration

import (
	"encoding/json"
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	caprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/render"
	"testing"
)

// A composite group of n images must draw every layer's motion from the pool
// reserved for n images and every layer must receive a different entrance. The
// old disjoint partition left the 3-, 4- and 5-image groups with only two
// motions, so larger composites silently repeated an entrance.
func TestGroupOwnedCompositeKeepsGroupMotionAndNoLayerMotions(t *testing.T) {
	layers := []capmodel.OverlayImageLayer{
		{ID: "a", AssetID: "a", StartMS: 0, EndMS: 3000, Caption: "Ada"},
		{ID: "b", AssetID: "b", StartMS: 0, EndMS: 3000, Caption: "Grace"},
		{ID: "c", AssetID: "c", StartMS: 0, EndMS: 3000, Caption: "Linus"},
	}
	items := []capmodel.OverlayItem{{
		ID: "group", SceneID: "scene", Kind: string(capmodel.KindEntityImage),
		TemplateID: "image_popup", StartMs: 0, EndMs: 3000,
		GroupMotionID: "trio_fan_reveal", ImageLayers: layers,
	}}
	if err := assignEntityImageMotions(items, 4, 1920, 1080, ""); err != nil {
		t.Fatalf("assign motions: %v", err)
	}
	if items[0].GroupMotionID != "trio_fan_reveal" {
		t.Fatalf("group motion was overwritten: %q", items[0].GroupMotionID)
	}
	for _, layer := range items[0].ImageLayers {
		if layer.MotionID != "" || layer.MotionParams != nil {
			t.Fatalf("group-owned layer %q received a competing motion: %+v", layer.ID, layer)
		}
		if layer.CaptionMotionID == "" {
			t.Fatalf("group-owned layer %q lost its caption entrance", layer.ID)
		}
	}
}

func TestGroupOwnedCompositeWireCarriesGroupMotion(t *testing.T) {
	plan := capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan, PlanID: "group-wire", VideoID: "video",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Items: []capmodel.OverlayItem{{
			ID: "group", SceneID: "scene", Kind: string(capmodel.KindEntityImage),
			TemplateID: "image_popup", StartMs: 0, EndMs: 5000,
			GroupMotionID: "trio_fan_reveal",
			ImageLayers: []capmodel.OverlayImageLayer{
				{ID: "a", AssetID: "a", StartMS: 0, EndMS: 5000},
				{ID: "b", AssetID: "b", StartMS: 0, EndMS: 5000},
				{ID: "c", AssetID: "c", StartMS: 0, EndMS: 5000},
			},
		}},
	}
	raw, err := marshalRenderingGenOverlayPlan(plan)
	if err != nil {
		t.Fatalf("marshal group plan: %v", err)
	}
	var wire struct {
		Items []struct {
			GroupMotionID string `json:"group_motion_id"`
			MotionID      string `json:"motion_id"`
			ImageLayers   []struct {
				MotionID string `json:"motion_id"`
			} `json:"image_layers"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode group wire: %v", err)
	}
	if len(wire.Items) != 1 || wire.Items[0].GroupMotionID != "trio_fan_reveal" || wire.Items[0].MotionID != "" {
		t.Fatalf("group choreography did not reach the render wire: %+v", wire.Items)
	}
	for _, layer := range wire.Items[0].ImageLayers {
		if layer.MotionID != "" {
			t.Fatalf("group-owned layer leaked a competing motion to the wire: %+v", layer)
		}
	}
}

func TestCompositeLayersUseTheirGroupSizePool(t *testing.T) {
	for size := 2; size <= 5; size++ {
		layers := make([]capmodel.OverlayImageLayer, size)
		assets := make([]capmodel.OverlayAssetRef, size)
		for i := range layers {
			id := fmt.Sprintf("layer-%d", i)
			layers[i] = capmodel.OverlayImageLayer{ID: id, AssetID: id, StartMS: 0, EndMS: 3000, PresetID: "image_focus_in"}
			assets[i] = capmodel.OverlayAssetRef{AssetID: id, SHA256: fmt.Sprintf("%064x", i+1)}
		}
		items := []capmodel.OverlayItem{{
			ID: fmt.Sprintf("group-%d", size), SceneID: "scene", Kind: string(capmodel.KindEntityImage),
			TemplateID: "image_popup", StartMs: 0, EndMs: 3000, AssetRefs: assets, ImageLayers: layers,
		}}
		if err := assignEntityImageMotions(items, 0, 1920, 1080, ""); err != nil {
			t.Fatalf("size %d: assign motions: %v", size, err)
		}
		pool := caprender.CompositeImageMotionPool(size)
		seen := make(map[string]string, size)
		for _, layer := range items[0].ImageLayers {
			if !containsString(pool, layer.MotionID) {
				t.Fatalf("size %d layer %q motion %q is outside its group pool", size, layer.ID, layer.MotionID)
			}
			if previous, repeated := seen[layer.MotionID]; repeated {
				t.Fatalf("size %d layers %q and %q share motion %q", size, previous, layer.ID, layer.MotionID)
			}
			seen[layer.MotionID] = layer.ID
		}
		if len(seen) != size {
			t.Fatalf("size %d assigned %d distinct motions, want %d", size, len(seen), size)
		}
	}
}
