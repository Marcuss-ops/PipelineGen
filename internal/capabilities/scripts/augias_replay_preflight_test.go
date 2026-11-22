package scriptgeneration

import (
	"fmt"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// TestAugiasFiveSceneReplayPassesImagePreflight closes the offline replay: the
// five materialized portraits from the catalog-lane test, expressed as the
// sealed overlay items the final job actually preflights, must pass the same
// gate that rejects the r10 silent-empty class.
func TestAugiasFiveSceneReplayPassesImagePreflight(t *testing.T) {
	items := make([]capmodel.OverlayItem, 0, 5)
	for i := 1; i <= 5; i++ {
		path, sha := preflightPNGFile(t)
		item := imagePreflightItem(fmt.Sprintf("scene-%02d-portrait", i), "entity_image", sha, "")
		item.SceneID = fmt.Sprintf("scene-%02d", i)
		item.AssetRefs[0].LocalPath = path
		items = append(items, item)
	}
	if err := finalJobImagePreflight(items, nil, true); err != nil {
		t.Fatalf("five verified Augias portraits must pass the image preflight: %v", err)
	}
}

// TestAugiasFiveSceneReplayPreflightFailsClosedWhenEveryRefIsUnverified keeps
// the gate honest: the same five-scene shape with unverifiable bytes (stale
// local copy, wrong content address) must fail before any render is queued.
func TestAugiasFiveSceneReplayPreflightFailsClosedWhenEveryRefIsUnverified(t *testing.T) {
	items := make([]capmodel.OverlayItem, 0, 5)
	for i := 1; i <= 5; i++ {
		path, _ := preflightPNGFile(t)
		item := capmodel.OverlayItem{
			ID:      fmt.Sprintf("scene-%02d-portrait", i),
			Kind:    "entity_image",
			SceneID: fmt.Sprintf("scene-%02d", i),
			AssetRefs: []capmodel.OverlayAssetRef{{
				AssetID:   fmt.Sprintf("asset-%d", i),
				SHA256:    strings.Repeat("a", 64), // valid shape, wrong bytes
				LocalPath: path,
			}},
		}
		items = append(items, item)
	}
	if err := finalJobImagePreflight(items, nil, true); err == nil {
		t.Fatal("five promised portraits with unverifiable bytes must fail the image preflight")
	}
	// The mirror-image regression: an image request that compiled down to zero
	// overlay items must fail too, instead of passing as an empty plan.
	if err := finalJobImagePreflight(nil, nil, true); err == nil {
		t.Fatal("an image request with zero compiled overlays must fail the preflight")
	}
	if err := finalJobImagePreflight(nil, nil, false); err != nil {
		t.Fatalf("a job that requested no images must pass an empty preflight: %v", err)
	}
}
