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
	// 66 = 34 Apple + 2 soft Brush accents + 5 semantic highlights + 15 native documentary presets + 10 text_3d_v1. The 3D
	// family is back after the renderer fix was frame-verified (2026-10-15);
	// the 2026-10-15 look-alike prune retired apple_focus_rise,
	// apple_hero_statement and glass_morphism_fade from it (kept:
	// apple_soft_scale, hero_scale_focus). Only retired motions stay out.
	if len(lines) != 66 {
		t.Fatalf("longpool has %d motions, want 66 (Apple + soft Brush + semantic highlights + 15 native documentary presets + fixed 3D long lane)", len(lines))
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
