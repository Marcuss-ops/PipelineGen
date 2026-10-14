package overlays

import (
	"os"
	"strings"
	"testing"
)

// The long-phrase motion catalog job renders one overlay per motion in
// ops/jobs/longpool.txt. Every id must be renderable (certified, not
// retired) AND long-lane compatible: the long selector only rotates the
// intersection of the caller pool with longPhraseMotionCandidates, so an
// id outside it would silently collapse the rotation and repeat motions.
func TestLongCatalogPoolIsLongLaneCompatible(t *testing.T) {
	raw, err := os.ReadFile("../../../ops/jobs/longpool.txt")
	if err != nil {
		t.Fatalf("read longpool: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	// 37 = the Apple-only long lane: the 10 text_3d_v1 entries render pure
	// black on the standalone overlay path (frame-verified), and the
	// typewriter/highlight/burst families were retired with the catalog,
	// so the catalog job rotates the surviving Apple inventories only.
	if len(lines) != 37 {
		t.Fatalf("longpool has %d motions, want 37 (Apple-only long lane)", len(lines))
	}
	certified := make(map[string]bool, len(CertifiedPhraseMotions()))
	for _, id := range CertifiedPhraseMotions() {
		certified[id] = true
	}
	long := make(map[string]bool, len(LongPhraseMotionCandidates()))
	for _, id := range LongPhraseMotionCandidates() {
		long[id] = true
	}
	seen := make(map[string]bool, len(lines))
	for i, line := range lines {
		id := strings.TrimSpace(line)
		if id == "" {
			t.Errorf("line %d is empty", i)
			continue
		}
		if seen[id] {
			t.Errorf("line %d repeats %q: rotation pool must be distinct", i, id)
		}
		seen[id] = true
		if !certified[id] {
			t.Errorf("line %d: %q is not a certified phrase motion", i, id)
		}
		if IsRemovedGeneratedPhraseMotion(id) {
			t.Errorf("line %d: %q was retired from generated overlays", i, id)
		}
		if !long[id] {
			t.Errorf("line %d: %q is outside the long-phrase lane (would collapse rotation)", i, id)
		}
	}
}
