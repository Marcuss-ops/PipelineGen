package overlays

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func captionedImagePlan() capmodel.OverlayPlan {
	return capmodel.OverlayPlan{
		Width: 1920, Height: 1080,
		Items: []capmodel.OverlayItem{{
			ID: "captioned-image", Kind: "image", EntityCaption: "Ada",
		}},
	}
}

func TestAssignPlannerItemMotionsUsesImageWithTextPool(t *testing.T) {
	pool := ImageWithTextMotionPool(0)
	if len(pool) < 2 {
		t.Fatalf("certified image-with-text pool has %d motions", len(pool))
	}

	pinned := captionedImagePlan()
	assignPlannerItemMotions(&pinned, PlanInput{
		PlanID: "pinned", VideoID: "video", Width: 1920, Height: 1080,
		ImageWithTextMotions: []string{pool[1]},
	}, "seed", "seed", "", nil)
	if pinned.Items[0].MotionID != pool[1] {
		t.Fatalf("captioned image motion = %q, want pinned %q", pinned.Items[0].MotionID, pool[1])
	}

	fallback := captionedImagePlan()
	assignPlannerItemMotions(&fallback, PlanInput{
		PlanID: "fallback", VideoID: "video", Width: 1920, Height: 1080,
	}, "seed", "seed", "", nil)
	if !containsMotion(pool, fallback.Items[0].MotionID) {
		t.Fatalf("captioned image motion = %q, want a certified pool member", fallback.Items[0].MotionID)
	}
}

func TestValidateImageWithTextMotionPoolFailsClosed(t *testing.T) {
	if err := validateImageWithTextMotionPool(nil); err != nil {
		t.Fatalf("empty pool must stay valid: %v", err)
	}
	pool := ImageWithTextMotionPool(0)
	if len(pool) == 0 {
		t.Fatal("certified image-with-text pool is empty")
	}
	if err := validateImageWithTextMotionPool([]string{pool[0]}); err != nil {
		t.Fatalf("certified id rejected: %v", err)
	}
	if err := validateImageWithTextMotionPool([]string{"definitely_not_a_motion"}); err == nil {
		t.Fatal("unknown id must fail closed")
	}
	if err := validateImageWithTextMotionPool([]string{pool[0], pool[0]}); err == nil {
		t.Fatal("duplicate id must fail closed")
	}
}
