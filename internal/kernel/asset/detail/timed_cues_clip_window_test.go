package detail

import (
	"testing"
)

// The rebase is the fix for source-absolute cues reaching clip consumers:
// a cue recorded at 146s of the source video must become 0..N ms of a clip
// that starts at 146s.
func TestRebaseCuesForClip_ShiftsSourceWindowOntoClipTimeline(t *testing.T) {
	cues := []TimedCue{
		{StartMs: 146_000, EndMs: 148_500, Text: "first"},
		{StartMs: 148_500, EndMs: 151_000, Text: "second"},
	}

	got := RebaseCuesForClip(cues, 146, 176)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].StartMs != 0 || got[0].EndMs != 2_500 {
		t.Errorf("cue 0 = [%d,%d], want [0,2500]", got[0].StartMs, got[0].EndMs)
	}
	if got[1].StartMs != 2_500 || got[1].EndMs != 5_000 {
		t.Errorf("cue 1 = [%d,%d], want [2500,5000]", got[1].StartMs, got[1].EndMs)
	}
	if got[0].Text != "first" {
		t.Errorf("text must be preserved, got %q", got[0].Text)
	}
	// The input must not be mutated: other callers (the video-level
	// transcript endpoint) share the same cue slices.
	if cues[0].StartMs != 146_000 {
		t.Errorf("input mutated: cues[0].StartMs = %d", cues[0].StartMs)
	}
}

// A cue straddling the clip start must clamp to 0 instead of going
// negative, and a cue straddling the clip end must clamp to the clip
// duration instead of overshooting it (validateASSFile rejects a last
// cue end beyond clipDurationMs+250ms).
func TestRebaseCuesForClip_ClampsBoundaryCues(t *testing.T) {
	cues := []TimedCue{
		{StartMs: 145_500, EndMs: 147_000, Text: "straddles start"},
		{StartMs: 175_000, EndMs: 177_900, Text: "straddles end"},
	}

	got := RebaseCuesForClip(cues, 146, 176)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].StartMs != 0 || got[0].EndMs != 1_000 {
		t.Errorf("start-straddling cue = [%d,%d], want [0,1000]", got[0].StartMs, got[0].EndMs)
	}
	if got[1].StartMs != 29_000 || got[1].EndMs != 30_000 {
		t.Errorf("end-straddling cue = [%d,%d], want [29000,30000]", got[1].StartMs, got[1].EndMs)
	}
	if got[1].EndMs > 30_000 {
		t.Errorf("clamped end %d exceeds the 30s clip duration", got[1].EndMs)
	}
}

// Cues entirely outside the window carry no visible frame range inside
// the clip and must be dropped, not emitted as zero-length rows.
func TestRebaseCuesForClip_DropsCuesOutsideWindow(t *testing.T) {
	cues := []TimedCue{
		{StartMs: 10_000, EndMs: 12_000, Text: "before clip"},
		{StartMs: 146_000, EndMs: 148_000, Text: "inside clip"},
		{StartMs: 400_000, EndMs: 402_000, Text: "after clip"},
	}

	got := RebaseCuesForClip(cues, 146, 176)

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (only the in-window cue)", len(got))
	}
	if got[0].Text != "inside clip" || got[0].StartMs != 0 {
		t.Errorf("got %+v, want the in-window cue at 0", got[0])
	}
}

// The whole-video contract (0/0) is the video-level transcript
// endpoint's window: source-video timings must pass through untouched.
func TestRebaseCuesForClip_WholeVideoWindowIsNoOp(t *testing.T) {
	cues := []TimedCue{{StartMs: 146_000, EndMs: 148_000, Text: "unchanged"}}

	for _, window := range [][2]int{{0, 0}, {0, 600}} {
		got := RebaseCuesForClip(cues, window[0], window[1])
		if len(got) != 1 || got[0].StartMs != 146_000 {
			t.Errorf("window %v: got %+v, want the untouched cue", window, got)
		}
	}
}

// An empty input keeps the caller's "no cues" sentinel semantics.
func TestRebaseCuesForClip_EmptyInput(t *testing.T) {
	if got := RebaseCuesForClip(nil, 146, 176); got != nil {
		t.Errorf("nil input must stay nil, got %+v", got)
	}
	if got := RebaseCuesForClip([]TimedCue{}, 146, 176); len(got) != 0 {
		t.Errorf("empty input must stay empty, got %+v", got)
	}
}
