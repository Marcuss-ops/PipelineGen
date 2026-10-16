package overlays

import "testing"

// Each composite group size gets its own non-empty entrance pool drawn from
// the certified entity image catalog, and no entrance serves two sizes.
func TestCompositeImageMotionPoolsAreDisjointPerGroupSize(t *testing.T) {
	catalog := CertifiedEntityImageMotions()
	owner := make(map[string]int)
	for size := minCompositeGroupSize; size <= maxCompositeGroupSize; size++ {
		pool := CompositeImageMotionPool(size)
		if len(pool) == 0 {
			t.Fatalf("size %d pool is empty", size)
		}
		for _, id := range pool {
			if !containsString(catalog, id) {
				t.Fatalf("size %d pool contains %q outside the certified catalog", size, id)
			}
			if other, taken := owner[id]; taken {
				t.Fatalf("motion %q is shared by sizes %d and %d", id, other, size)
			}
			owner[id] = size
		}
	}
}

// Sizes outside 2..5 are not composite groups and keep the full catalog.
func TestCompositeImageMotionPoolFallsBackOutsideGroupRange(t *testing.T) {
	catalog := CertifiedEntityImageMotions()
	for _, size := range []int{0, 1, 6} {
		if got := CompositeImageMotionPool(size); len(got) != len(catalog) {
			t.Fatalf("size %d pool has %d motions, want full catalog %d", size, len(got), len(catalog))
		}
	}
}
