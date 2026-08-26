package voiceover

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// TestEdgeTTS_WordBoundariesComeFromSameSynthesisStream pins the single-pass
// Edge contract: ONE synthesis call yields BOTH the audio bytes and the
// WordBoundary stream, so the published timing.json derives from the exact
// same stream as the audio — never a second synthesis and never a separate
// transcription pass. A Whisper/second-pass path would surface as a second
// Synthesize call and is forbidden.
func TestEdgeTTS_WordBoundariesComeFromSameSynthesisStream(t *testing.T) {
	stagingDir := t.TempDir()
	tts := &goldenSinglePassTTS{dir: t.TempDir()}
	pub := &goldenPublisher{}

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: openProcessTestDB(t)},
		Finalizer:           &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "vo-stream"}},
		Logger:              zap.NewNop(),
	})

	const text = "Jackie Chan became known around the world."
	out, err := uc.Execute(context.Background(), &ProcessSegmentCommand{
		ID:       "vo-stream",
		JobID:    "job-stream",
		Text:     text,
		TextHash: TextHash(digest.SHA256String(text)),
		Language: "en",
		Voice:    "en-US-RogerNeural",
		Filename: "scene-0.mp3",
		Timing: &audio.TimingRequest{
			Mode:         audio.TimingRequired,
			BoundaryMode: audio.BoundaryWord,
			Formats:      []audio.TimingFormat{audio.TimingJSON},
		},
		Dest: &ResolvedDestination{FolderID: "folder-stream", FolderPath: stagingDir},
	})
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, out.Status)
	require.NotNil(t, out.Timing)

	// Exactly ONE synthesis call: the audio and the WordBoundary stream must
	// come from the same pass (zero transcription).
	require.Len(t, tts.synthesized, 1, "exactly one synthesis; boundaries must come from the same stream as the audio")

	// The published timing.json must be byte-identical to the boundaries the
	// single call produced (same stream — no re-synthesis drift). The golden
	// provider emits one 100ms word per whitespace-delimited token.
	words := strings.Fields(text)
	require.NotEmpty(t, words)

	var artifact audio.SpeechTimingArtifact
	require.NoError(t, json.Unmarshal(pub.files["scene-0-timing.json"], &artifact))
	require.Equal(t, len(words), len(artifact.Words), "published word count must match the single synthesis stream")
	for i, w := range words {
		require.Equal(t, w, artifact.Words[i].Text, "word %d text must match the synthesis stream", i)
		require.Equal(t, int64(i)*100_000, artifact.Words[i].StartUS, "word %d start must match the synthesis stream", i)
		require.Equal(t, int64(i+1)*100_000, artifact.Words[i].EndUS, "word %d end must match the synthesis stream", i)
	}
}
