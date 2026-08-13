package voiceover

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func writeTestAudio(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audio.mp3")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTimingArtifact_TextSHAExactSynthesizedText(t *testing.T) {
	text := "Il celebre incontro di Teano con re Vittorio Emanuele II."
	audioPath := writeTestAudio(t, "mp3-bytes")
	artifact, err := BuildTimingArtifact(text, "edge_tts", "it", "it-IT-DiegoNeural", audioPath, 3_210_000, []RawSpeechBoundary{
		{Text: "Il", StartUS: 0, EndUS: 125_000},
		{Text: "celebre", StartUS: 125_000, EndUS: 487_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(text))
	if artifact.TextSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("text hash = %s, want sha256(text)", artifact.TextSHA256)
	}
}

func TestTimingArtifact_AudioSHAFinalAudio(t *testing.T) {
	audioContent := "final-mp3-bytes"
	audioPath := writeTestAudio(t, audioContent)
	artifact, err := BuildTimingArtifact("text", "edge_tts", "en", "", audioPath, 100_000, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(audioContent))
	if artifact.AudioSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("audio hash = %s, want sha256(file bytes)", artifact.AudioSHA256)
	}
}

func TestTimingArtifact_StableCanonicalJSON(t *testing.T) {
	audioPath := writeTestAudio(t, "mp3-bytes")
	build := func() *audio.SpeechTimingArtifact {
		artifact, err := BuildTimingArtifact("Ciao mondo.", "edge_tts", "it", "it-IT-DiegoNeural", audioPath, 500_000, []RawSpeechBoundary{
			{Text: "Ciao", StartUS: 0, EndUS: 200_000},
			{Text: "mondo.", StartUS: 200_000, EndUS: 500_000},
		})
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

func TestTimingArtifact_UnicodeItalian(t *testing.T) {
	audioPath := writeTestAudio(t, "mp3-bytes")
	artifact, err := BuildTimingArtifact("l'Italia è bellissima — davvero!", "edge_tts", "it", "it-IT-DiegoNeural", audioPath, 900_000, []RawSpeechBoundary{
		{Text: "l'Italia", StartUS: 0, EndUS: 300_000},
		{Text: "è", StartUS: 300_000, EndUS: 400_000},
		{Text: "bellissima", StartUS: 400_000, EndUS: 800_000},
		{Text: "—", StartUS: 800_000, EndUS: 850_000},
		{Text: "davvero!", StartUS: 850_000, EndUS: 900_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Words[0].Text != "l'Italia" || artifact.Words[1].Text != "è" {
		t.Fatalf("unicode word text mangled: %+v", artifact.Words)
	}
}

func TestTimingArtifact_EmptyBoundariesProducesValidArtifact(t *testing.T) {
	audioPath := writeTestAudio(t, "mp3-bytes")
	artifact, err := BuildTimingArtifact("no timing", "edge_tts", "en", "", audioPath, 0, nil)
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

func TestTimingArtifact_RejectsInvalidBoundaries(t *testing.T) {
	audioPath := writeTestAudio(t, "mp3-bytes")
	// Non-monotonic: word 2 starts before word 1 ends.
	_, err := BuildTimingArtifact("a b c", "edge_tts", "en", "", audioPath, 1_000_000, []RawSpeechBoundary{
		{Text: "a", StartUS: 0, EndUS: 100_000},
		{Text: "b", StartUS: 50_000, EndUS: 200_000},
		{Text: "c", StartUS: 200_000, EndUS: 300_000},
	})
	if err == nil || !strings.Contains(err.Error(), "timing artifact") {
		t.Fatalf("expected fail-closed validation error, got %v", err)
	}
}

func TestTimingArtifact_MissingAudioFailsClosed(t *testing.T) {
	if _, err := BuildTimingArtifact("text", "edge_tts", "en", "", filepath.Join(t.TempDir(), "missing.mp3"), 0, nil); err == nil {
		t.Fatal("missing audio file must fail closed")
	}
}
