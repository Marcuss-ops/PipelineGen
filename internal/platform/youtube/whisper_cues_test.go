// Package youtube — whisper_cues_test.go: contract tests for the ASR-side
// cleanup applied to Whisper output before it becomes subtitles.
package youtube

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

func TestCleanASRText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "drops a music tag", in: "[Music]", want: ""},
		{name: "drops a blank-audio tag", in: "[BLANK_AUDIO]", want: ""},
		{name: "strips music symbols", in: "♪ hello ♪", want: "hello"},
		{name: "strips the speaker marker", in: ">> hello there", want: "hello there"},
		{name: "strips an inline applause tag", in: "Hello there [applause] friend", want: "Hello there friend"},
		{name: "strips a parenthesised tag", in: "(applause) bravo", want: "bravo"},
		{name: "collapses whitespace", in: "  spaced   text  ", want: "spaced text"},
		{name: "orphan punctuation collapses to empty", in: "...", want: ""},
		{name: "keeps real prose", in: "Dolly Parton got kicked out of a hotel.", want: "Dolly Parton got kicked out of a hotel."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CleanASRText(tc.in); got != tc.want {
				t.Fatalf("CleanASRText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanWhisperCues_DropsNoiseAndDuplicates(t *testing.T) {
	cues := []detail.TimedCue{
		{StartMs: 0, EndMs: 1000, Text: "[Music]"},
		{StartMs: 1000, EndMs: 2000, Text: "♪♪"},
		{StartMs: 2000, EndMs: 3000, Text: "Hello there [applause] friend"},
		{StartMs: 3000, EndMs: 4000, Text: "Hello there friend"}, // immediate duplicate
		{StartMs: 4000, EndMs: 4000, Text: "zero-width window"},  // invalid window
		{StartMs: 5000, EndMs: 6000, Text: ">> second speaker"},  // marker stripped
	}
	got := CleanWhisperCues(cues)
	if len(got) != 2 {
		t.Fatalf("cleaned cues = %d, want 2: %+v", len(got), got)
	}
	if got[0].Text != "Hello there friend" || got[0].StartMs != 2000 || got[0].EndMs != 3000 {
		t.Fatalf("cue 0 = %+v, want the cleaned speech cue at its own window", got[0])
	}
	if got[1].Text != "second speaker" || got[1].StartMs != 5000 {
		t.Fatalf("cue 1 = %+v, want the second speaker cue", got[1])
	}
}

func TestCleanWhisperCues_AllNoiseYieldsNil(t *testing.T) {
	got := CleanWhisperCues([]detail.TimedCue{
		{StartMs: 0, EndMs: 1000, Text: "[Music]"},
		{StartMs: 1000, EndMs: 2000, Text: "♪"},
	})
	if got != nil {
		t.Fatalf("all-noise input must yield nil, got %+v", got)
	}
}

func TestCleanWhisperCues_KeepsADistantRepeat(t *testing.T) {
	got := CleanWhisperCues([]detail.TimedCue{
		{StartMs: 0, EndMs: 1000, Text: "no no"},
		{StartMs: 5000, EndMs: 6000, Text: "no no"},
	})
	if len(got) != 2 {
		t.Fatalf("a repeat outside the hallucination window must survive: %+v", got)
	}
}

func TestRestoreSentenceCase(t *testing.T) {
	cases := []struct {
		name string
		in   string
		lang string
		want string
	}{
		{name: "capitalizes sentences", in: "hello world. the next one. and done", lang: "en", want: "Hello world. The next one. And done"},
		{name: "does not capitalize after an abbreviation", in: "see e.g. the manual and more", lang: "en", want: "See e.g. the manual and more"},
		{name: "already cased is stable", in: "Hello world. The next one", lang: "en", want: "Hello world. The next one"},
		{name: "cyrillic is cased", in: "привет мир. как дела", lang: "ru", want: "Привет мир. Как дела"},
		{name: "uncased script is untouched", in: "hello world", lang: "ja", want: "hello world"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RestoreSentenceCase(tc.in, tc.lang); got != tc.want {
				t.Fatalf("RestoreSentenceCase(%q, %q) = %q, want %q", tc.in, tc.lang, got, tc.want)
			}
		})
	}
}

func TestJoinCueTexts(t *testing.T) {
	got := JoinCueTexts([]detail.TimedCue{
		{Text: "one"},
		{Text: "  "},
		{Text: "two"},
	})
	if got != "one two" {
		t.Fatalf("JoinCueTexts = %q, want %q", got, "one two")
	}
}
