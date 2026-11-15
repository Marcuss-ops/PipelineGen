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
		Title:      "MILTON LEITE: a decisao do STJ",
		OutputName: "MILTON LEITE: a decisao do STJ",
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

// TestFinalJobVideoNameSanitizesNonASCII pins the ASCII-only Master identity:
// the 2026-11-14 live run with an em-dash title stalled the artifact-upload ack
// for 300 s; the same plan with an ASCII title completed in 1.37 s.
func TestFinalJobVideoNameSanitizesNonASCII(t *testing.T) {
	req := GenerateRequest{Title: "Subs style E2E — Poppins, stroke e shadow"}
	const runID = "run_a9a76f28-80c1-4f3a-92c4-0ce8070128d5"

	got := finalJobVideoName(req, runID)
	for _, r := range got {
		if r > 0x7f {
			t.Fatalf("video_name %q contains non-ASCII rune %q", got, r)
		}
	}
	if !strings.HasPrefix(got, "Subs style E2E - Poppins, stroke e shadow [") {
		t.Fatalf("video_name = %q, want the sanitized readable prefix", got)
	}
	if !strings.Contains(got, finalJobIdentitySuffix(runID)) {
		t.Fatalf("video_name = %q, want the run-scoped suffix", got)
	}

	if onlyUnsafe := finalJobVideoName(GenerateRequest{Title: "—"}, runID); !strings.HasPrefix(onlyUnsafe, "final-job [") {
		t.Fatalf("video_name = %q, want the final-job fallback", onlyUnsafe)
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
