package overlays

import "testing"

// TestSelectMapCompositionIsDeterministicPerSeed pins the seed→composition
// contract of the planner's draw: the same job always records the same
// composition, whatever the servable subset is.
func TestSelectMapCompositionIsDeterministicPerSeed(t *testing.T) {
	first := SelectMapComposition("job-a", "scene-1", "item-1", 2)
	second := SelectMapComposition("job-a", "scene-1", "item-1", 2)
	if first != second {
		t.Fatalf("same seed must give the same composition: %q vs %q", first, second)
	}
}

// TestSelectMapCompositionDrawsOnlyServableFamilies pins the map-audit
// selection rule: a random draw only ever selects compositions the single
// dynamic renderer serves (PortedMapCompositions). While every composed
// family is demolition debt awaiting its own parity gate (the native renderer
// serves the base flyover only), the draw is empty and the map presents the
// certified base flyover — an unported family is excluded from random
// selection, never silently swapped in and never a failed map.
func TestSelectMapCompositionDrawsOnlyServableFamilies(t *testing.T) {
	ported := map[string]bool{}
	for _, id := range PortedMapCompositions() {
		ported[id] = true
	}
	for i := 0; i < 64; i++ {
		seed := string(rune('a'+i%26)) + "-job-" + string(rune('0'+i%10)) + string(rune('A'+i%7))
		for _, pins := range []int{1, 2, 5} {
			got := SelectMapComposition(seed, "scene-1", "item-1", pins)
			if got != "" && !ported[got] {
				t.Fatalf("selection %q (seed %q, %d pins) is not servable by the single dynamic renderer", got, seed, pins)
			}
		}
	}
}

// TestSelectMapCompositionExcludesIncompatibleFamilies pins the data
// compatibility half of the selection contract: one map holds 1..5 grounded
// places (the renderer's pin budget), and anything outside never receives a
// composition.
func TestSelectMapCompositionExcludesIncompatibleFamilies(t *testing.T) {
	ported := map[string]bool{}
	for _, id := range PortedMapCompositions() {
		ported[id] = true
	}
	for i := 0; i < 64; i++ {
		seed := string(rune('a' + i%26))
		single := SelectMapComposition(seed, "scene-1", "item-1", 1)
		if single != "" && !ported[single] {
			t.Fatalf("one pin must draw only servable compositions, got %q", single)
		}
	}
	if got := SelectMapComposition("job", "scene-1", "item-1", 0); got != "" {
		t.Fatalf("zero pins must not receive a composition, got %q", got)
	}
	if got := SelectMapComposition("job", "scene-1", "item-1", maxMapPinsPerGroup+1); got != "" {
		t.Fatalf("pins above the group cap must not receive a composition, got %q", got)
	}
}
