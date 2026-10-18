package overlays

import "testing"

func TestSelectMapCompositionIsDeterministicPerSeed(t *testing.T) {
	first := SelectMapComposition("job-a", "scene-1", "item-1", 2)
	second := SelectMapComposition("job-a", "scene-1", "item-1", 2)
	if first == "" || first != second {
		t.Fatalf("same seed must give the same composition: %q vs %q", first, second)
	}
}

func TestSelectMapCompositionVariesAcrossSeeds(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		seed := string(rune('a'+i%26)) + "-job-" + string(rune('0'+i%10)) + string(rune('A'+i%7))
		seen[SelectMapComposition(seed, "scene-1", "item-1", 2)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("different seeds should yield varied compositions, got %v", seen)
	}
}

func TestSelectMapCompositionExcludesIncompatibleFamilies(t *testing.T) {
	for i := 0; i < 64; i++ {
		seed := string(rune('a' + i%26))
		single := SelectMapComposition(seed, "scene-1", "item-1", 1)
		if single == "pulse_marker" {
			t.Fatalf("pulse_marker is multi-pin and must not be chosen for one pin (seed %q)", seed)
		}
		if single != "region_zoom" && single != "spotlight" {
			t.Fatalf("one pin must use the location_focus family, got %q", single)
		}
	}
	if got := SelectMapComposition("job", "scene-1", "item-1", 0); got != "" {
		t.Fatalf("zero pins must not receive a composition, got %q", got)
	}
	if got := SelectMapComposition("job", "scene-1", "item-1", maxMapPinsPerGroup+1); got != "" {
		t.Fatalf("pins above the group cap must not receive a composition, got %q", got)
	}
}
