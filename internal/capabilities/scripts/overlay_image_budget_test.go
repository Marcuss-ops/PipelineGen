package scriptgeneration

import "testing"

// TestResolveImageOverlayCeilingRejectsDisagreeingBudgets pins the two-knob
// contract: max_image_overlays and overlay_group_limits.images are the same
// run-level budget, so an explicit disagreement is an error instead of a
// silent group override (the r8 12-vs-3 shape).
func TestResolveImageOverlayCeilingRejectsDisagreeingBudgets(t *testing.T) {
	if _, err := resolveImageOverlayCeiling(12, map[string]int{overlayGroupImages: 3}); err == nil {
		t.Fatal("max 12 with group 3 must fail instead of silently rendering 3")
	}
	if got, err := resolveImageOverlayCeiling(12, map[string]int{overlayGroupImages: 12}); err != nil || got != 12 {
		t.Fatalf("agreeing budgets: got %d, err %v", got, err)
	}
	if got, err := resolveImageOverlayCeiling(0, map[string]int{overlayGroupImages: 3}); err != nil || got != 3 {
		t.Fatalf("group-only budget: got %d, err %v", got, err)
	}
	if got, err := resolveImageOverlayCeiling(12, nil); err != nil || got != 12 {
		t.Fatalf("max-only budget: got %d, err %v", got, err)
	}
	if got, err := resolveImageOverlayCeiling(0, map[string]int{overlayGroupImages: 0}); err != nil || got != 0 {
		t.Fatalf("disabled group keeps the certified default: got %d, err %v", got, err)
	}
}
