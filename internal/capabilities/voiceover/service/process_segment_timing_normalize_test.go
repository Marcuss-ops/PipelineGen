package voiceover

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func TestNormalizeFinalWordTiming_ClampsTrailingWordStartedJustPastAudio(t *testing.T) {
	words := []audio.SpeechWordTiming{
		{Index: 0, Text: "uno", StartUS: 0, EndUS: 100_000},
		{Index: 1, Text: "due", StartUS: 100_000, EndUS: 200_000},
		{Index: 2, Text: "tre", StartUS: 200_008, EndUS: 200_008},
	}
	got, dropped, clamped := normalizeFinalWordTiming(words, 200_000)
	if dropped != 0 || clamped != 0 {
		t.Fatalf("dropped=%d clamped=%d, want dropped=0 clamped=0", dropped, clamped)
	}
	if len(got) != 3 {
		t.Fatalf("got %d words, want 3", len(got))
	}
	if got[2].StartUS != 200_000 || got[2].EndUS != 200_000 {
		t.Fatalf("trailing word span=[%d,%d], want [200000,200000]", got[2].StartUS, got[2].EndUS)
	}
	if _, err := audio.BuildSpeechTimingArtifact("edge_tts", "it", "", "text-sha", "audio-sha", 200_000, got); err != nil {
		t.Fatalf("normalized timing must validate: %v", err)
	}
}

func TestNormalizeFinalWordTiming_DropsTrailingWordFarPastAudio(t *testing.T) {
	words := []audio.SpeechWordTiming{
		{Index: 0, Text: "uno", StartUS: 0, EndUS: 100_000},
		{Index: 1, Text: "due", StartUS: 100_000, EndUS: 200_000},
		{Index: 2, Text: "tre", StartUS: 2_200_000, EndUS: 2_300_000},
	}
	got, dropped, _ := normalizeFinalWordTiming(words, 200_000)
	if dropped != 1 || len(got) != 2 {
		t.Fatalf("dropped=%d words=%d, want dropped=1 words=2", dropped, len(got))
	}
}

// TestNormalizeFinalWordTiming_ClampsLiveProviderDrift pins the live failure
// that aborted s3/en: the final word ended 777_825us past a 67_512_000us audio,
// 27_825us beyond the former 750ms ceiling.
func TestNormalizeFinalWordTiming_ClampsLiveProviderDrift(t *testing.T) {
	words := []audio.SpeechWordTiming{
		{Index: 0, Text: "uno", StartUS: 0, EndUS: 67_300_000},
		{Index: 1, Text: "due", StartUS: 67_300_000, EndUS: 68_289_825},
	}
	got, dropped, clamped := normalizeFinalWordTiming(words, 67_512_000)
	if dropped != 0 || clamped != 777_825 {
		t.Fatalf("dropped=%d clamped=%d, want dropped=0 clamped=777825", dropped, clamped)
	}
	if _, err := audio.BuildSpeechTimingArtifact("edge_tts", "en", "", "text-sha", "audio-sha", 67_512_000, got); err != nil {
		t.Fatalf("live provider drift must validate after normalization: %v", err)
	}
}

// TestNormalizeFinalWordTiming_ClampsPenultimateOverhang pins the s3/en r3
// failure: the trailing word starts after the audio and is collapsed to a
// zero-length span, while the previous word still ends past the duration.
func TestNormalizeFinalWordTiming_ClampsPenultimateOverhang(t *testing.T) {
	words := []audio.SpeechWordTiming{
		{Index: 0, Text: "uno", StartUS: 0, EndUS: 66_000_000},
		{Index: 1, Text: "due", StartUS: 66_000_000, EndUS: 66_107_323},
		{Index: 2, Text: "tre", StartUS: 66_110_000, EndUS: 66_120_000},
	}
	got, dropped, clamped := normalizeFinalWordTiming(words, 66_024_000)
	if dropped != 0 || clamped != 83_323 {
		t.Fatalf("dropped=%d clamped=%d, want dropped=0 clamped=83323", dropped, clamped)
	}
	if _, err := audio.BuildSpeechTimingArtifact("edge_tts", "en", "", "text-sha", "audio-sha", 66_024_000, got); err != nil {
		t.Fatalf("penultimate overhang must validate after normalization: %v", err)
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
