package voiceover

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func TestNormalizeFinalWordTiming_DropsTrailingWordStartedPastAudio(t *testing.T) {
	words := []audio.SpeechWordTiming{
		{Index: 0, Text: "uno", StartUS: 0, EndUS: 100_000},
		{Index: 1, Text: "due", StartUS: 100_000, EndUS: 200_000},
		{Index: 2, Text: "tre", StartUS: 200_008, EndUS: 200_008},
	}
	got, dropped, clamped := normalizeFinalWordTiming(words, 200_000)
	if dropped != 1 || clamped != 0 {
		t.Fatalf("dropped=%d clamped=%d, want dropped=1 clamped=0", dropped, clamped)
	}
	if len(got) != 2 {
		t.Fatalf("got %d words, want 2", len(got))
	}
	if _, err := audio.BuildSpeechTimingArtifact("edge_tts", "it", "", "text-sha", "audio-sha", 200_000, got); err != nil {
		t.Fatalf("normalized timing must validate: %v", err)
	}
}

func TestNormalizeFinalWordTiming_ClampsSmallFinalOverrun(t *testing.T) {
	words := []audio.SpeechWordTiming{
		{Index: 0, Text: "uno", StartUS: 0, EndUS: 100_000},
		{Index: 1, Text: "due", StartUS: 100_000, EndUS: 200_008},
	}
	got, dropped, clamped := normalizeFinalWordTiming(words, 200_000)
	if dropped != 0 || clamped != 8 {
		t.Fatalf("dropped=%d clamped=%d, want dropped=0 clamped=8", dropped, clamped)
	}
	if got[len(got)-1].EndUS != 200_000 {
		t.Fatalf("final end=%d, want 200000", got[len(got)-1].EndUS)
	}
	if _, err := audio.BuildSpeechTimingArtifact("edge_tts", "it", "", "text-sha", "audio-sha", 200_000, got); err != nil {
		t.Fatalf("normalized timing must validate: %v", err)
	}
}
