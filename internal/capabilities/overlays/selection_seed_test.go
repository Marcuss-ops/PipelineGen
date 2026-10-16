package overlays

import "testing"

// P1-2: unico owner del seeding — deterministico e bounded.
func TestSeededGroupIndexUnified(t *testing.T) {
	a := SeededGroupIndex("seed", "image", "g1", 7)
	b := SeededGroupIndex("seed", "image", "g1", 7)
	if a != b || a < 0 || a >= 7 {
		t.Fatalf("deterministic bounded index, got %d vs %d", a, b)
	}
	if SeededGroupIndex("s", "t", "g", 1) != 0 || SeededGroupIndex("s", "t", "g", 0) != 0 {
		t.Fatalf("n<=1 must be 0")
	}
}
