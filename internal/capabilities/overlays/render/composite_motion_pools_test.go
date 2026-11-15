package render

import capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"

import "testing"

// Every composite group size gets the complete certified catalog rotated from
// a size-specific start. The first `size` entries must therefore be distinct:
// a 5-image group cannot deliver five independent entrances from a two-motion
// slice, which is exactly the regression this test pins.
func TestCompositeImageMotionPoolsCoverEveryGroupSizeWithoutRepeats(t *testing.T) {
	catalog := capoverlay.CertifiedEntityImageMotions()
	if len(catalog) < maxCompositeGroupSize {
		t.Fatalf("certified catalog has %d motions, need at least %d", len(catalog), maxCompositeGroupSize)
	}
	starts := map[string]int{}
	for size := minCompositeGroupSize; size <= maxCompositeGroupSize; size++ {
		pool := CompositeImageMotionPool(size)
		if len(pool) != len(catalog) {
			t.Fatalf("size %d pool has %d motions, want the complete catalog (%d)", size, len(pool), len(catalog))
		}
		seen := map[string]bool{}
		first := map[string]bool{}
		for index, id := range pool {
			if !containsString(catalog, id) {
				t.Fatalf("size %d pool contains %q outside the certified catalog", size, id)
			}
			if seen[id] {
				t.Fatalf("size %d pool repeats %q", size, id)
			}
			seen[id] = true
			if index < size {
				if first[id] {
					t.Fatalf("size %d repeats motion %q inside its first %d entrances", size, id, size)
				}
				first[id] = true
			}
		}
		if len(first) != size {
			t.Fatalf("size %d covers %d distinct entrances, want %d", size, len(first), size)
		}
		if other, exists := starts[pool[0]]; exists {
			t.Fatalf("sizes %d and %d start on the same motion %q", other, size, pool[0])
		}
		starts[pool[0]] = size
	}
}

// Sizes outside 2..5 are not composite groups and keep the full catalog.
func TestCompositeImageMotionPoolFallsBackOutsideGroupRange(t *testing.T) {
	catalog := capoverlay.CertifiedEntityImageMotions()
	for _, size := range []int{0, 1, 6} {
		if got := CompositeImageMotionPool(size); len(got) != len(catalog) {
			t.Fatalf("size %d pool has %d motions, want full catalog %d", size, len(got), len(catalog))
		}
	}
}
