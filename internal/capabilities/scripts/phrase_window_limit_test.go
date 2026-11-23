package scriptgeneration

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// TestPhraseWindowLimitClampsToNextItem pins the normal overlap guard: a
// phrase may use the free space up to the next overlay, never beyond it.
func TestPhraseWindowLimitClampsToNextItem(t *testing.T) {
	plan := capmodel.OverlayPlan{
		DurationMS: 90_000,
		Items: []capmodel.OverlayItem{
			{ID: "phrase", StartUS: 10_000_000, DurationUS: 8_000_000},
			// StartUSValue falls back to StartMs when DurationUS is zero.
			{ID: "next", StartUS: 30_000_000, StartMs: 30_000, DurationUS: 1},
		},
	}
	if got := phraseWindowLimitUS(plan, "phrase", 10_000_000, 8_000_000); got != 20_000_000 {
		t.Fatalf("phrase window = %dus, want 20s up to the next item", got)
	}
}

// TestPhraseWindowLimitClampsToPlanEndWhenSpokenWindowEscapesPlan pins the
// R33 shape: a 4,075-frame (169.8s at 24fps) phrase starting near the end of a
// 90s plan must be clamped to the plan remainder. Returning the spoken window
// commissioned a clip longer than the scene and stalled the scheduler.
func TestPhraseWindowLimitClampsToPlanEndWhenSpokenWindowEscapesPlan(t *testing.T) {
	const spokenUS = int64(169_800_000) // 4,075 frames at 24 fps
	plan := capmodel.OverlayPlan{
		DurationMS: 90_000,
		Items: []capmodel.OverlayItem{
			{ID: "phrase", StartUS: 88_000_000, DurationUS: spokenUS},
		},
	}
	if got := phraseWindowLimitUS(plan, "phrase", 88_000_000, spokenUS); got != 2_000_000 {
		t.Fatalf("phrase window = %dus, want the 2s plan remainder", got)
	}
}

// TestPhraseWindowLimitKeepsSpokenWindowWithoutAPlanBoundary pins the only
// case where the spoken window is the answer: neither a following item nor a
// plan duration bounds the phrase.
func TestPhraseWindowLimitKeepsSpokenWindowWithoutAPlanBoundary(t *testing.T) {
	plan := capmodel.OverlayPlan{
		Items: []capmodel.OverlayItem{{ID: "phrase", StartUS: 0, DurationUS: 8_000_000}},
	}
	if got := phraseWindowLimitUS(plan, "phrase", 0, 8_000_000); got != 8_000_000 {
		t.Fatalf("phrase window = %dus, want the uncapped 8s spoken window", got)
	}
}
