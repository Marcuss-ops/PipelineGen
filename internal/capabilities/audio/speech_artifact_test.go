package audio

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	testTextSHA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testAudioSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func speechArtifactWords() []SpeechWordTiming {
	return []SpeechWordTiming{
		{Index: 0, Text: "Il", StartUS: 0, EndUS: 125_000},
		{Index: 1, Text: "celebre", StartUS: 125_000, EndUS: 487_000},
		{Index: 2, Text: "incontro", StartUS: 487_000, EndUS: 1_020_000},
		{Index: 3, Text: "di", StartUS: 1_020_000, EndUS: 1_350_000},
		{Index: 4, Text: "Teano", StartUS: 1_350_000, EndUS: 2_430_000},
	}
}

func TestSpeechArtifact_BindsHashesVerbatim(t *testing.T) {
	artifact, err := BuildSpeechTimingArtifact("edge_tts", "it", "it-IT-DiegoNeural", testTextSHA, testAudioSHA, 2_430_000, speechArtifactWords())
	if err != nil {
		t.Fatal(err)
	}
	if artifact.TextSHA256 != testTextSHA || artifact.AudioSHA256 != testAudioSHA {
		t.Fatalf("hashes not bound verbatim: %+v", artifact)
	}
	if artifact.Provider != "edge_tts" || artifact.BoundaryMode != BoundaryWord {
		t.Fatalf("identity fields lost: %+v", artifact)
	}
	if err := artifact.Validate(); err != nil {
		t.Fatalf("built artifact must validate: %v", err)
	}
}

func TestSpeechArtifact_RequiresHashes(t *testing.T) {
	if _, err := BuildSpeechTimingArtifact("edge_tts", "it", "", "", testAudioSHA, 0, nil); err == nil {
		t.Fatal("empty text_sha256 must fail closed")
	}
	if _, err := BuildSpeechTimingArtifact("edge_tts", "it", "", testTextSHA, "", 0, nil); err == nil {
		t.Fatal("empty audio_sha256 must fail closed")
	}
}

func TestSpeechArtifact_StableCanonicalJSON(t *testing.T) {
	build := func() *SpeechTimingArtifact {
		artifact, err := BuildSpeechTimingArtifact("edge_tts", "it", "it-IT-DiegoNeural", testTextSHA, testAudioSHA, 2_430_000, speechArtifactWords())
		if err != nil {
			t.Fatal(err)
		}
		return artifact
	}
	first, err := json.Marshal(build())
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(build())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical JSON is not stable:\n%s\nvs\n%s", first, second)
	}
}

func TestSpeechArtifact_UnicodeItalian(t *testing.T) {
	artifact, err := BuildSpeechTimingArtifact("edge_tts", "it", "it-IT-DiegoNeural", testTextSHA, testAudioSHA, 900_000, []SpeechWordTiming{
		{Index: 0, Text: "l'Italia", StartUS: 0, EndUS: 300_000},
		{Index: 1, Text: "è", StartUS: 300_000, EndUS: 400_000},
		{Index: 2, Text: "bellissima", StartUS: 400_000, EndUS: 800_000},
		{Index: 3, Text: "—", StartUS: 800_000, EndUS: 850_000},
		{Index: 4, Text: "davvero!", StartUS: 850_000, EndUS: 900_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Words[0].Text != "l'Italia" || artifact.Words[1].Text != "è" {
		t.Fatalf("unicode word text mangled: %+v", artifact.Words)
	}
}

func TestSpeechArtifact_EmptyBoundariesProducesValidArtifact(t *testing.T) {
	artifact, err := BuildSpeechTimingArtifact("edge_tts", "en", "", testTextSHA, testAudioSHA, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact.Words) != 0 {
		t.Fatalf("expected empty words, got %d", len(artifact.Words))
	}
	if err := artifact.Validate(); err != nil {
		t.Fatalf("empty-boundary artifact must validate: %v", err)
	}
}

func TestSpeechArtifact_RejectsInvalidBoundaries(t *testing.T) {
	// Non-monotonic: word 2 starts before word 1 ends.
	_, err := BuildSpeechTimingArtifact("edge_tts", "en", "", testTextSHA, testAudioSHA, 1_000_000, []SpeechWordTiming{
		{Index: 0, Text: "a", StartUS: 0, EndUS: 100_000},
		{Index: 1, Text: "b", StartUS: 50_000, EndUS: 200_000},
		{Index: 2, Text: "c", StartUS: 200_000, EndUS: 300_000},
	})
	if err == nil || !strings.Contains(err.Error(), "speech timing artifact") {
		t.Fatalf("expected fail-closed validation error, got %v", err)
	}
}

func TestSpeechArtifact_DoesNotAliasInputSlice(t *testing.T) {
	words := speechArtifactWords()
	artifact, err := BuildSpeechTimingArtifact("edge_tts", "it", "", testTextSHA, testAudioSHA, 2_430_000, words)
	if err != nil {
		t.Fatal(err)
	}
	words[0].StartUS = 999_999
	if artifact.Words[0].StartUS == 999_999 {
		t.Fatal("builder aliased the caller's slice")
	}
}
