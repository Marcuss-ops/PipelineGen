package scriptgeneration

import (
	"fmt"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// Generated entity portraits stay within the certified 2D image pool: the
// full layer-only image vocabulary, none of the camera-backed 3D families.
// Entity captions stay on the four certified 2D caption motions.
//
// The pool size is asserted as a literal (10 images, 4 captions) rather than
// derived from the pool under test, so this gate actually fails when the
// catalog drifts. TestEntityImageMotionRotationCoversTheCertifiedCatalog in
// package overlays pins the same ten ids against the master image catalog.
func TestAssignEntityImageMotionsUsesRestrainedCatalog(t *testing.T) {
	const wantImages, wantCaptions = 10, 4

	certified := capabilityoverlay.CertifiedEntityImageMotions()
	captionCertified := capabilityoverlay.CertifiedEntityCaptionMotions()
	if len(captionCertified) != wantCaptions {
		t.Fatalf("generated entity caption catalog has %d motions, want %d", len(captionCertified), wantCaptions)
	}
	if len(certified) != wantImages {
		t.Fatalf("generated entity image catalog has %d motions, want %d", len(certified), wantImages)
	}

	// One single-portrait item per certified motion so a full rotation has
	// enough slots to reach every id, plus one composite pair whose two
	// children must keep independent motions.
	items := make([]capabilityoverlay.OverlayItem, 0, wantImages+1)
	for i := 0; i < wantImages; i++ {
		items = append(items, capabilityoverlay.OverlayItem{
			ID: fmt.Sprintf("portrait-%d", i), Kind: string(capabilityoverlay.KindEntityImage),
		})
	}
	items = append(items, capabilityoverlay.OverlayItem{
		ID: "pair", Kind: string(capabilityoverlay.KindEntityImage),
		ImageLayers: []capabilityoverlay.OverlayImageLayer{{ID: "a"}, {ID: "b"}},
	})
	assignEntityImageMotions(items, 0, 1920, 1080)

	captionSeen := map[string]bool{}
	seen := map[string]bool{}
	composite := map[string]bool{}
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
				composite[layer.MotionID] = true
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
	if len(seen) != wantImages {
		t.Fatalf("%d entity images selected %d distinct motions, want catalog rotation across %d", len(items), len(seen), wantImages)
	}
	if len(captionSeen) != wantCaptions {
		t.Fatalf("%d entity captions selected %d distinct motions, want rotation across %d", len(items), len(captionSeen), wantCaptions)
	}
	// The two composite children are assigned independently, so the pair must
	// not collapse onto a single shared entrance.
	if len(composite) != 2 {
		t.Fatalf("composite children used %d distinct motions, want 2 independent assignments", len(composite))
	}
}
