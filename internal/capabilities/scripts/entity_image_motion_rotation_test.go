package scriptgeneration

import (
	"fmt"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// Generated entity portraits stay within the restrained, certified 2D pool.
func TestAssignEntityImageMotionsUsesRestrainedCatalog(t *testing.T) {
	items := make([]capabilityoverlay.OverlayItem, 0, 5)
	for i := 0; i < 4; i++ {
		items = append(items, capabilityoverlay.OverlayItem{
			ID: fmt.Sprintf("portrait-%d", i), Kind: string(capabilityoverlay.KindEntityImage),
		})
	}
	items = append(items, capabilityoverlay.OverlayItem{
		ID: "pair", Kind: string(capabilityoverlay.KindEntityImage),
		ImageLayers: []capabilityoverlay.OverlayImageLayer{{ID: "a"}, {ID: "b"}},
	})
	assignEntityImageMotions(items, 0, 1920, 1080)

	certified := capabilityoverlay.CertifiedEntityImageMotions()
	captionCertified := capabilityoverlay.CertifiedEntityCaptionMotions()
	captionSeen := map[string]bool{}
	if len(captionCertified) != 4 {
		t.Fatalf("generated entity caption catalog has %d motions, want 4", len(captionCertified))
	}
	if len(certified) != 3 {
		t.Fatalf("generated entity image catalog has %d motions, want 3", len(certified))
	}
	seen := map[string]bool{}
	for _, item := range items {
		if len(item.ImageLayers) > 0 {
			for _, layer := range item.ImageLayers {
				if layer.MotionID == "" {
					t.Fatalf("composite child lost its motion")
				}
				if !containsMotionID(certified, layer.MotionID) {
					t.Fatalf("composite child motion %q is outside the certified catalog", layer.MotionID)
				}
				seen[layer.MotionID] = true
				if layer.CaptionMotionID == "" || !containsMotionID(captionCertified, layer.CaptionMotionID) {
					t.Fatalf("composite caption motion %q is not certified", layer.CaptionMotionID)
				}
				captionSeen[layer.CaptionMotionID] = true
			}
			continue
		}
		if item.CaptionMotionID == "" {
			t.Fatalf("entity image %q lost its caption motion", item.ID)
		}
		if !containsMotionID(captionCertified, item.CaptionMotionID) {
			t.Fatalf("entity image %q caption motion %q is outside the certified catalog", item.ID, item.CaptionMotionID)
		}
		captionSeen[item.CaptionMotionID] = true
		if item.MotionID == "" {
			t.Fatalf("entity image %q lost its motion", item.ID)
		}
		if !containsMotionID(certified, item.MotionID) {
			t.Fatalf("entity image %q motion %q is outside the certified catalog", item.ID, item.MotionID)
		}
		seen[item.MotionID] = true
	}
	if len(seen) != len(certified) {
		t.Fatalf("six entity images selected %d distinct motions, want catalog rotation across %d", len(seen), len(certified))
	}
	if len(captionSeen) != len(captionCertified) {
		t.Fatalf("six entity captions selected %d distinct motions, want rotation across %d", len(captionSeen), len(captionCertified))
	}
}
