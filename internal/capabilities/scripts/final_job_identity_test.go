package scriptgeneration

import (
	"strings"
	"testing"
)

// TestFinalJobVideoNameIsRunScoped pins the fix for the replace_overlap 422:
// the Master submission identity (video_name) is derived from the run, not from
// the caller's title/project. Two runs of the same topic share a title, so a
// title-only identity collides; the run-scoped suffix keeps them distinct while
// staying stable for retries of the same run.
func TestFinalJobVideoNameIsRunScoped(t *testing.T) {
	req := GenerateRequest{
		Title:      "MILTON LEITE: a decisão do STJ",
		OutputName: "MILTON LEITE: a decisão do STJ",
	}
	const runOne = "run_1790318876036014800_2a5c45bb"
	const runTwo = "run_1790420124179913810_a294cb"

	first := finalJobVideoName(req, runOne)
	second := finalJobVideoName(req, runTwo)
	if first == second {
		t.Fatalf("different runs with the same title must not collide: %q", first)
	}
	if !strings.HasPrefix(first, req.Title) {
		t.Fatalf("video_name = %q, want the readable title prefix", first)
	}
	if !strings.Contains(first, finalJobIdentitySuffix(runOne)) {
		t.Fatalf("video_name = %q, want the run-scoped suffix", first)
	}
	if again := finalJobVideoName(req, runOne); again != first {
		t.Fatalf("video_name must be stable for the same run: %q vs %q", again, first)
	}
	// A missing run id must not fabricate a suffix (the fallback chain already
	// terminates in a constant, and the caller guarantees a run id in practice).
	if got := finalJobVideoName(GenerateRequest{Title: "Dolly"}, ""); got != "Dolly" {
		t.Fatalf("empty run id must keep the base name, got %q", got)
	}
}

// TestFinalJobIdentitySuffixSanitizes confirms the token is filesystem/URL safe
// and bounded regardless of the run id shape.
func TestFinalJobIdentitySuffixSanitizes(t *testing.T) {
	if got := finalJobIdentitySuffix("run/with:bad chars"); strings.ContainsAny(got, "/: ") {
		t.Fatalf("suffix %q must not contain unsafe characters", got)
	}
	if got := finalJobIdentitySuffix("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); len(got) != 16 {
		t.Fatalf("suffix length = %d, want 16", len(got))
	}
	if got := finalJobIdentitySuffix(""); got != "" {
		t.Fatalf("empty run id suffix = %q, want empty", got)
	}
}
