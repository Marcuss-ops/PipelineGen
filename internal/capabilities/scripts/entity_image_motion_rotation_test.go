package scriptgeneration

import (
	"fmt"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// Generated entity portraits and their captions rotate over the certified
// automatic-runtime subcatalogs. Pool sizes are literal drift gates because
// they define what the planner emits across the producer→renderer boundary.
func TestAssignEntityImageMotionsUsesCertifiedCatalog(t *testing.T) {
	const wantImages, wantCaptions = 5, 56

	certified := capabilityoverlay.CertifiedEntityImageMotions()
	captionCertified := capabilityoverlay.CertifiedEntityCaptionMotions()
	certifiedCaptionSet := make(map[string]bool, len(captionCertified))
	if len(captionCertified) != wantCaptions {
		t.Fatalf("generated entity caption catalog has %d motions, want %d", len(captionCertified), wantCaptions)
	}
	if len(certified) != wantImages {
		t.Fatalf("generated entity image catalog has %d motions, want %d", len(certified), wantImages)
	}
	for _, id := range captionCertified {
		if certifiedCaptionSet[id] {
			t.Fatalf("caption catalog contains duplicate motion %q", id)
		}
		certifiedCaptionSet[id] = true
	}

	// Enough captioned portraits to exhaust both bounded image and broad
	// caption catalogs, plus one composite pair whose children rotate separately.
	portraitCount := wantCaptions - 2
	items := make([]capabilityoverlay.OverlayItem, 0, portraitCount+1)
	for i := 0; i < portraitCount; i++ {
		items = append(items, capabilityoverlay.OverlayItem{
			ID: fmt.Sprintf("portrait-%d", i), Kind: string(capabilityoverlay.KindEntityImage), EntityCaption: fmt.Sprintf("Person %d", i),
		})
	}
	items = append(items, capabilityoverlay.OverlayItem{
		ID: "pair", Kind: string(capabilityoverlay.KindEntityImage),
		ImageLayers: []capabilityoverlay.OverlayImageLayer{{ID: "a", Caption: "Person A"}, {ID: "b", Caption: "Person B"}},
	})
	assignEntityImageMotions(items, 0, 1920, 1080, "")

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
				wantCaption := captionCertified[len(captionSeen)]
				if layer.CaptionMotionID != wantCaption {
					t.Fatalf("composite caption motion %q, want no-repeat catalog motion %q", layer.CaptionMotionID, wantCaption)
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
		wantCaption := captionCertified[len(captionSeen)]
		if item.CaptionMotionID != wantCaption {
			t.Fatalf("entity caption motion %q, want no-repeat catalog motion %q", item.CaptionMotionID, wantCaption)
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

func TestAssignEntityImageMotionsUsesSeparatePoolForImagesWithText(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "named-image-a", Kind: "image", EntityCaption: "São Paulo", CaptionMotionID: "typewriter_clean"},
		{ID: "named-image-b", Kind: "image", EntityCaption: "Brasília", CaptionMotionID: "text_yaw_in"},
	}
	assignEntityImageMotions(items, 0, 1920, 1080, "")
	textPool := capabilityoverlay.CertifiedImageWithTextMotions()
	for i := 0; i < 2; i++ {
		if items[i].MotionID == "" || !containsMotionID(textPool, items[i].MotionID) {
			t.Fatalf("image with text %q got uncertified motion %q", items[i].ID, items[i].MotionID)
		}
		if items[i].CaptionMotionID == "" || !containsMotionID(capabilityoverlay.CertifiedEntityCaptionMotions(), items[i].CaptionMotionID) {
			t.Fatalf("image with text %q got uncertified caption motion %q", items[i].ID, items[i].CaptionMotionID)
		}
	}
	if items[0].MotionID == items[1].MotionID {
		t.Fatalf("image with text rotation reused %q", items[0].MotionID)
	}
}
