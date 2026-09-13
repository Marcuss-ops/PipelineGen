package texttracks

// shortform_cues_test.go locks the short-form caption contract:
//   - a long Whisper-style segment is split into readable captions,
//   - the words are never reordered, dropped or duplicated,
//   - the split only redistributes the source window (captions stay in sync
//     with the audio),
//   - short captions are extended instead of flickering,
//   - an already-readable transcript passes through untouched.

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// assertReadable pins the whole short-form contract on a normalized track.
func assertReadable(t *testing.T, policy ShortFormPolicy, out []detail.TimedCue) {
	t.Helper()
	for i, c := range out {
		dur := c.EndMs - c.StartMs
		if dur <= 0 {
			t.Fatalf("caption %d has an empty window: %+v", i, c)
		}
		if got := wrappedLines(c.Text, policy.MaxCharsPerLine); got > policy.MaxLines {
			t.Fatalf("caption %d wraps to %d lines, want <= %d: %q", i, got, policy.MaxLines, c.Text)
		}
		chars := utf8.RuneCountInString(c.Text)
		if chars > policy.MaxCharsPerLine && dur > policy.readingMs(chars) {
			t.Fatalf("caption %d dwells %dms for %d chars (reading time %dms): %q", i, dur, chars, policy.readingMs(chars), c.Text)
		}
		if i > 0 && c.StartMs < out[i-1].EndMs {
			t.Fatalf("captions %d/%d overlap: %+v", i-1, i, out)
		}
	}
}

func wordsOf(in []detail.TimedCue) []string {
	var out []string
	for _, c := range in {
		out = append(out, strings.Fields(c.Text)...)
	}
	return out
}

func TestNormalizeShortFormCues_SplitsLongWhisperSegment(t *testing.T) {
	// The real failure: a 7.44s Whisper segment carrying 95 runes wrapped to
	// three lines and stayed on screen for seven seconds.
	source := []detail.TimedCue{{
		StartMs: 13040,
		EndMs:   20480,
		Text:    "this is fine and I did it and but I was in Dubai in 2004 and went to the top of this building",
	}}
	out := NormalizeShortFormCues(source, ShortFormPolicy{})
	if len(out) < 2 {
		t.Fatalf("long segment produced %d captions, want at least 2: %+v", len(out), out)
	}
	policy := DefaultShortFormPolicy()
	assertReadable(t, policy, out)
	for i, c := range out {
		if c.StartMs < source[0].StartMs || c.EndMs > source[0].EndMs {
			t.Fatalf("caption %d escaped the source window [%d,%d]: %+v", i, source[0].StartMs, source[0].EndMs, c)
		}
	}
	if got, want := strings.Join(wordsOf(out), " "), strings.Join(strings.Fields(source[0].Text), " "); got != want {
		t.Fatalf("text changed:\n got %q\nwant %q", got, want)
	}
}

func TestNormalizeShortFormCues_PreservesReadableTranscript(t *testing.T) {
	source := []detail.TimedCue{
		{StartMs: 0, EndMs: 2000, Text: "Okay, no."},
		{StartMs: 2000, EndMs: 4000, Text: "I'm on the go."},
		{StartMs: 4000, EndMs: 5000, Text: "How does that even work?"},
	}
	out := NormalizeShortFormCues(source, ShortFormPolicy{})
	if len(out) != len(source) {
		t.Fatalf("got %d captions, want %d: %+v", len(out), len(source), out)
	}
	for i := range source {
		if out[i].StartMs != source[i].StartMs || out[i].EndMs != source[i].EndMs || out[i].Text != source[i].Text {
			t.Fatalf("caption %d changed:\n got %+v\nwant %+v", i, out[i], source[i])
		}
	}
}

func TestNormalizeShortFormCues_ClosesSubSecondGap(t *testing.T) {
	source := []detail.TimedCue{
		{StartMs: 0, EndMs: 3000, Text: "first caption here"},
		{StartMs: 3200, EndMs: 6000, Text: "second caption here"},
	}
	out := NormalizeShortFormCues(source, ShortFormPolicy{})
	if len(out) != 2 {
		t.Fatalf("got %d captions, want 2: %+v", len(out), out)
	}
	if out[0].EndMs != out[1].StartMs {
		t.Fatalf("200ms gap survived: %+v", out)
	}
}

func TestNormalizeShortFormCues_KeepsRealSilence(t *testing.T) {
	source := []detail.TimedCue{
		{StartMs: 0, EndMs: 3000, Text: "first caption here"},
		{StartMs: 6200, EndMs: 9000, Text: "second caption here"},
	}
	out := NormalizeShortFormCues(source, ShortFormPolicy{})
	if len(out) != 2 {
		t.Fatalf("got %d captions, want 2: %+v", len(out), out)
	}
	if out[0].EndMs >= out[1].StartMs {
		t.Fatalf("a 3.2s silence must stay silent: %+v", out)
	}
}

// TestNormalizeShortFormCues_RealWhisperSegments locks the contract on the
// segments that produced the bad clips: Whisper windows of 3-7.4s carrying up
// to 135 runes (three to four wrapped lines).
func TestNormalizeShortFormCues_RealWhisperSegments(t *testing.T) {
	source := []detail.TimedCue{
		{StartMs: 0, EndMs: 4800, Text: "Where he can tell what colored underwear a man is wearing just from just from looking at"},
		{StartMs: 5440, EndMs: 12920, Text: "You do not disappoint. Oh, man. Listen, and this is any scraping the surface, buddy. This is okay"},
		{StartMs: 16120, EndMs: 18800, Text: "Should I do me to do me to do it to you?"},
		{StartMs: 18800, EndMs: 23720, Text: "I have so many I have so many just you facing the wrong way on a toilet"},
		{StartMs: 23720, EndMs: 30799, Text: "You just naked in an empty bathtub. It's great. I am just just the water way to start the day the water slowly running around"},
	}
	policy := DefaultShortFormPolicy()
	out := NormalizeShortFormCues(source, policy)
	if len(out) < len(source)*2 {
		t.Fatalf("got %d captions from %d segments, want at least %d", len(out), len(source), len(source)*2)
	}
	assertReadable(t, policy, out)
	if got, want := strings.Join(wordsOf(out), " "), strings.Join(wordsOf(source), " "); got != want {
		t.Fatalf("text changed:\n got %q\nwant %q", got, want)
	}
}

func TestNormalizeShortFormCues_Deterministic(t *testing.T) {
	source := []detail.TimedCue{
		{StartMs: 0, EndMs: 5200, Text: "where he can tell what colored underwear a man is wearing just from looking at him"},
		{StartMs: 5440, EndMs: 12920, Text: "you do not disappoint oh man listen and this is any scraping the surface"},
	}
	first := NormalizeShortFormCues(source, ShortFormPolicy{})
	second := NormalizeShortFormCues(source, ShortFormPolicy{})
	if len(first) != len(second) {
		t.Fatalf("non-deterministic caption count: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("caption %d differs between runs: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func TestNormalizeShortFormCues_MergesSubSecondCaption(t *testing.T) {
	// A 200ms caption is a flash; its text fits the neighbour's budget, so the
	// two are merged into one readable caption instead of blinking.
	source := []detail.TimedCue{
		{StartMs: 0, EndMs: 1000, Text: "first"},
		{StartMs: 1100, EndMs: 1300, Text: "flash"},
	}
	out := NormalizeShortFormCues(source, ShortFormPolicy{})
	if len(out) != 1 {
		t.Fatalf("got %d captions, want 1 (merged): %+v", len(out), out)
	}
	if out[0].Text != "first flash" || out[0].StartMs != 0 || out[0].EndMs != 1300 {
		t.Fatalf("merged caption = %+v, want {0..1300 \"first flash\"}", out[0])
	}
	if out[0].EndMs-out[0].StartMs < DefaultShortFormPolicy().MinCueMs {
		t.Fatalf("merged caption still flashes: %+v", out[0])
	}
}

func TestNormalizeShortFormCues_EmptyInput(t *testing.T) {
	if out := NormalizeShortFormCues(nil, ShortFormPolicy{}); out != nil {
		t.Fatalf("nil input produced %+v", out)
	}
	if out := NormalizeShortFormCues([]detail.TimedCue{{StartMs: 0, EndMs: 0, Text: "x"}}, ShortFormPolicy{}); len(out) != 0 {
		t.Fatalf("degenerate input produced %+v", out)
	}
}
