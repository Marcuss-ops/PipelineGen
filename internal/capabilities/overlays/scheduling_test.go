package overlays

import "testing"

func TestSchedulingPrioritiesUrgentWorkWins(t *testing.T) {
	if RenderPriority(true) <= PreparePriority(true) || PreparePriority(true) <= RenderPriority(false) || RenderPriority(false) <= PreparePriority(false) {
		t.Fatalf("unexpected priority ordering: current render=%d current prepare=%d future render=%d future prepare=%d", RenderPriority(true), PreparePriority(true), RenderPriority(false), PreparePriority(false))
	}
}
