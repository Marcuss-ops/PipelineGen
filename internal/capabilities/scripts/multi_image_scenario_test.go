package scriptgeneration

import (
	"fmt"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// imageOnlyItems builds count image-only entity items in one scene, starting
// at startMs and spaced stepMs apart, each with its own asset.
func imageOnlyItems(count int, startMs, stepMs int64) []capabilityoverlay.OverlayItem {
	items := make([]capabilityoverlay.OverlayItem, 0, count)
	for i := 0; i < count; i++ {
		start := startMs + int64(i)*stepMs
		items = append(items, capabilityoverlay.OverlayItem{
			ID: fmt.Sprintf("img-%d", i), SceneID: "scene", Kind: string(capabilityoverlay.KindEntityImage),
			TemplateID: "image_popup", PresetID: "image_focus_in", StartMs: start, EndMs: start + 2000,
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: fmt.Sprintf("asset-%d", i), SHA256: fmt.Sprintf("%064x", i+1), URL: fmt.Sprintf("https://example.test/%d.jpg", i)}},
		})
	}
	return items
}

// Scenarios: how image-only items at different spacings are emitted, and which
// motions each layer receives. Close items (within the 5 s mention gap) must
// become one composite of their own size; distant items stay separate.
func TestImageOnlyScenariosGroupAndAnimateByCloseness(t *testing.T) {
	cases := []struct {
		name       string
		count      int
		stepMs     int64
		wantItems  int
		wantLayers int
	}{
		{"one image", 1, 0, 1, 0},
		{"two close", 2, 1000, 1, 2},
		{"three close", 3, 1000, 1, 3},
		{"five close", 5, 800, 1, 5},
		{"two far apart", 2, 6000, 2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := composeNearbyEntityImages(imageOnlyItems(tc.count, 0, tc.stepMs), 1920, 1080)
			if len(items) != tc.wantItems {
				t.Fatalf("%s: %d overlay items, want %d", tc.name, len(items), tc.wantItems)
			}
			if tc.wantLayers == 0 {
				for _, item := range items {
					if len(item.ImageLayers) != 0 {
						t.Fatalf("%s: separate item %q unexpectedly composited", tc.name, item.ID)
					}
				}
				t.Logf("%s -> %d separate overlays", tc.name, len(items))
				return
			}
			if err := assignEntityImageMotions(items, 0, 1920, 1080, ""); err != nil {
				t.Fatalf("%s: assign motions: %v", tc.name, err)
			}
			composite := items[0]
			if len(composite.ImageLayers) != tc.wantLayers {
				t.Fatalf("%s: composite has %d layers, want %d", tc.name, len(composite.ImageLayers), tc.wantLayers)
			}
			pool := capabilityoverlay.CompositeImageMotionPool(tc.wantLayers)
			for _, layer := range composite.ImageLayers {
				if !containsString(pool, layer.MotionID) {
					t.Fatalf("%s: layer %q motion %q outside its size pool", tc.name, layer.ID, layer.MotionID)
				}
				t.Logf("%s -> layer %s motion=%s window=[%d,%d)", tc.name, layer.ID, layer.MotionID, layer.StartMS, layer.EndMS)
			}
		})
	}
}
