package audio

import (
	"strings"
	"testing"
)

// subtitleTestArtifact mirrors the design-doc example: two readable cues
// (5 words each) from word-level timing.
func subtitleTestArtifact() SpeechTimingArtifact {
	return SpeechTimingArtifact{
		Version:      SpeechTimingVersion,
		Provider:     "edge_tts",
		BoundaryMode: BoundaryWord,
		Language:     "it",
		Voice:        "it-IT-DiegoNeural",
		TextSHA256:   strings.Repeat("a", 64),
		AudioSHA256:  strings.Repeat("b", 64),
		DurationUS:   3_210_000,
		Words: []SpeechWordTiming{
			{Index: 0, Text: "Il", StartUS: 0, EndUS: 125_000},
			{Index: 1, Text: "celebre", StartUS: 125_000, EndUS: 487_000},
			{Index: 2, Text: "incontro", StartUS: 487_000, EndUS: 1_020_000},
			{Index: 3, Text: "di", StartUS: 1_020_000, EndUS: 1_350_000},
			{Index: 4, Text: "Teano", StartUS: 1_350_000, EndUS: 2_430_000},
			{Index: 5, Text: "con", StartUS: 2_430_000, EndUS: 2_560_000},
			{Index: 6, Text: "re", StartUS: 2_560_000, EndUS: 2_710_000},
			{Index: 7, Text: "Vittorio", StartUS: 2_710_000, EndUS: 2_980_000},
			{Index: 8, Text: "Emanuele", StartUS: 2_980_000, EndUS: 3_100_000},
			{Index: 9, Text: "II", StartUS: 3_100_000, EndUS: 3_210_000},
		},
	}
}

func TestSRT_FromCanonicalTiming(t *testing.T) {
	srt, err := RenderSRT(subtitleTestArtifact(), CueOptions{MaxWords: 5})
	if err != nil {
		t.Fatal(err)
	}
	text := string(srt)
	want := "1\n00:00:00,000 --> 00:00:02,430\nIl celebre incontro di Teano\n\n" +
		"2\n00:00:02,430 --> 00:00:03,210\ncon re Vittorio Emanuele II\n\n"
	if text != want {
		t.Fatalf("SRT mismatch:\ngot:\n%s\nwant:\n%s", text, want)
	}
}

func TestVTT_FromCanonicalTiming(t *testing.T) {
	vtt, err := RenderVTT(subtitleTestArtifact(), CueOptions{MaxWords: 5})
	if err != nil {
		t.Fatal(err)
	}
	text := string(vtt)
	want := "WEBVTT\n\n" +
		"00:00:00.000 --> 00:00:02.430\nIl celebre incontro di Teano\n\n" +
		"00:00:02.430 --> 00:00:03.210\ncon re Vittorio Emanuele II\n\n"
	if text != want {
		t.Fatalf("VTT mismatch:\ngot:\n%s\nwant:\n%s", text, want)
	}
}

func TestSRTAndVTT_SameSemanticIntervals(t *testing.T) {
	artifact := subtitleTestArtifact()
	// Both projections must group into the SAME cues (same text + times)
	// — the only difference is the timestamp syntax.
	if _, err := RenderSRT(artifact, CueOptions{MaxWords: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := RenderVTT(artifact, CueOptions{MaxWords: 5}); err != nil {
		t.Fatal(err)
	}
	cues, err := BuildCues(artifact, CueOptions{MaxWords: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 {
		t.Fatalf("want 2 cues, got %d", len(cues))
	}
	if cues[0].Text != "Il celebre incontro di Teano" || cues[0].StartUS != 0 || cues[0].EndUS != 2_430_000 {
		t.Fatalf("cue 1 = %+v", cues[0])
	}
	if cues[1].Text != "con re Vittorio Emanuele II" || cues[1].StartUS != 2_430_000 || cues[1].EndUS != 3_210_000 {
		t.Fatalf("cue 2 = %+v", cues[1])
	}
}

func TestFormatters_DoNotMutateArtifact(t *testing.T) {
	artifact := subtitleTestArtifact()
	before := artifact.DeepCopy()
	if _, err := RenderSRT(artifact, CueOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := RenderVTT(artifact, CueOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(before.Words) != len(artifact.Words) || before.Words[0] != artifact.Words[0] {
		t.Fatal("renderers mutated the artifact")
	}
}

func TestBuildCues_SentenceEndBreaks(t *testing.T) {
	artifact := subtitleTestArtifact()
	artifact.Words[4].Text = "Teano."
	cues, err := BuildCues(artifact, CueOptions{MaxWords: 100, MaxChars: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 {
		t.Fatalf("sentence end must break the cue: got %d cues", len(cues))
	}
	if cues[0].Text != "Il celebre incontro di Teano." {
		t.Fatalf("cue 1 = %q", cues[0].Text)
	}
	if cues[1].Text != "con re Vittorio Emanuele II" {
		t.Fatalf("cue 2 = %q", cues[1].Text)
	}
}

func TestRenderers_EmptyTimingProducesNoCues(t *testing.T) {
	artifact := subtitleTestArtifact()
	artifact.Words = nil
	srt, err := RenderSRT(artifact, CueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(srt) != 0 {
		t.Fatalf("empty timing must render empty SRT, got %q", srt)
	}
}
