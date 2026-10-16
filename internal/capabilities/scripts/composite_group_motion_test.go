package scriptgeneration

import (
	"fmt"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// A composite group of n images must draw every layer's motion from the pool
// reserved for n images, so a 2-image group, a 3-image group, and so on never
// share an entrance family.
func TestCompositeLayersUseTheirGroupSizePool(t *testing.T) {
	for size := 2; size <= 5; size++ {
		layers := make([]capabilityoverlay.OverlayImageLayer, size)
		assets := make([]capabilityoverlay.OverlayAssetRef, size)
		for i := range layers {
			id := fmt.Sprintf("layer-%d", i)
			layers[i] = capabilityoverlay.OverlayImageLayer{ID: id, AssetID: id, StartMS: 0, EndMS: 3000, PresetID: "image_focus_in"}
			assets[i] = capabilityoverlay.OverlayAssetRef{AssetID: id, SHA256: fmt.Sprintf("%064x", i+1)}
		}
		items := []capabilityoverlay.OverlayItem{{
			ID: fmt.Sprintf("group-%d", size), SceneID: "scene", Kind: string(capabilityoverlay.KindEntityImage),
			TemplateID: "image_popup", StartMs: 0, EndMs: 3000, AssetRefs: assets, ImageLayers: layers,
		}}
		if err := assignEntityImageMotions(items, 0, 1920, 1080, ""); err != nil {
			t.Fatalf("size %d: assign motions: %v", size, err)
		}
		pool := capabilityoverlay.CompositeImageMotionPool(size)
		for _, layer := range items[0].ImageLayers {
			if !containsString(pool, layer.MotionID) {
				t.Fatalf("size %d layer %q motion %q is outside its group pool", size, layer.ID, layer.MotionID)
			}
		}
	}
}
