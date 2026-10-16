package overlays

import "testing"

// The long lane is a projection of the shared registry. It must contain only
// active, renderer-supported phrase IDs, with no duplicates or side manifest.
func TestLongCatalogPoolIsRegistryBacked(t *testing.T) {
	pool := LongPhraseMotionCandidates()
	if len(pool) == 0 {
		t.Fatal("long phrase lane has no registered motions")
	}
	seen := make(map[string]bool, len(pool))
	for _, id := range pool {
		if seen[id] {
			t.Errorf("long phrase lane repeats %q", id)
		}
		seen[id] = true
		if !AnimationSelectable(id, "phrase.long") {
			t.Errorf("long phrase lane contains unregistered or unsupported motion %q", id)
		}
		if IsRemovedGeneratedPhraseMotion(id) {
			t.Errorf("long phrase lane contains retired motion %q", id)
		}
	}
}
