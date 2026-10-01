package adapters

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestVidRushImageTargetHonorsPlanImagesPerScene pins the images_per_scene
// contract: an explicit plan cadence drives the per-scene target even above
// the historical default of two, the default applies only when an image
// provider is enabled but no cadence was requested, and a plan that asks for
// no images stays at zero.
func TestVidRushImageTargetHonorsPlanImagesPerScene(t *testing.T) {
	if got := vidRushImageTarget(&scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 5}); got != 5 {
		t.Fatalf("explicit ImagesPerScene target = %d, want 5", got)
	}
	internetOnly := &scriptpkg.ResolvedGenerationPlan{MediaPlan: media.MediaPlanSpec{
		ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
	}}
	if got := vidRushImageTarget(internetOnly); got != vidRushDefaultImagesPerScene {
		t.Fatalf("default image target = %d, want %d", got, vidRushDefaultImagesPerScene)
	}
	if got := vidRushImageTarget(nil); got != 0 {
		t.Fatalf("nil plan image target = %d, want 0", got)
	}
	if got := vidRushImageTarget(&scriptpkg.ResolvedGenerationPlan{}); got != 0 {
		t.Fatalf("provider-less plan image target = %d, want 0", got)
	}
}
