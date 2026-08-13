// Package voiceover — timing_observability_test.go (PR-VOICEOVER-TIMING-OBS).
//
// Pins the structured voiceover.timing.* lifecycle events: capture.started,
// capture.completed, normalized, published and failed must be emitted with
// summary metadata only (scene_id, language, provider, boundary_mode,
// word_count, duration_us) and must NEVER leak the per-word array.
package voiceover

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// timingMessages filters an observed log capture down to the
// voiceover.timing.* event names, in emission order.
func timingMessages(observed *observer.ObservedLogs) []string {
	var out []string
	for _, e := range observed.All() {
		if strings.HasPrefix(e.Message, "voiceover.timing.") {
			out = append(out, e.Message)
		}
	}
	return out
}

func TestTimingObservabilityEvents_Lifecycle(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	log := zap.New(core)

	tts := &goldenSinglePassTTS{dir: t.TempDir()}
	pub := &goldenPublisher{}
	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: openProcessTestDB(t)},
		Finalizer:           &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "vo-obs"}},
		Logger:              log,
	})

	out, err := uc.Execute(context.Background(), &ProcessSegmentCommand{
		ID:       "vo-obs",
		JobID:    "job-obs",
		Text:     "Il celebre incontro di Teano.",
		Language: "it",
		Voice:    "it-IT-DiegoNeural",
		Filename: "scene-0.mp3",
		Timing: &audio.TimingRequest{
			Mode:         audio.TimingRequired,
			BoundaryMode: audio.BoundaryWord,
			Formats:      []audio.TimingFormat{audio.TimingJSON, audio.TimingSRT, audio.TimingVTT},
		},
		Dest: &ResolvedDestination{FolderID: "folder-obs", FolderPath: t.TempDir()},
	})
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, out.Status)

	// Exact lifecycle order, with no extra timing events.
	assert.Equal(t, []string{
		"voiceover.timing.capture.started",
		"voiceover.timing.capture.completed",
		"voiceover.timing.normalized",
		"voiceover.timing.published",
	}, timingMessages(observed))

	// "Il celebre incontro di Teano." → 5 words × 100ms.
	const wantWords = int64(5)
	const wantDurationUS = int64(500_000)

	for _, e := range observed.All() {
		if !strings.HasPrefix(e.Message, "voiceover.timing.") {
			continue
		}
		ctx := e.ContextMap()
		assert.Equal(t, "vo-obs", ctx["scene_id"], "event %s scene_id", e.Message)
		assert.Equal(t, "it", ctx["language"], "event %s language", e.Message)

		switch e.Message {
		case "voiceover.timing.capture.completed", "voiceover.timing.normalized", "voiceover.timing.published":
			assert.Equal(t, "edge_tts", ctx["provider"], "event %s provider", e.Message)
			assert.Equal(t, "word", ctx["boundary_mode"], "event %s boundary_mode", e.Message)
			assert.Equal(t, wantWords, ctx["word_count"], "event %s word_count", e.Message)
			assert.Equal(t, wantDurationUS, ctx["duration_us"], "event %s duration_us", e.Message)
		}

		// Never leak individual words: no field value may equal any of the
		// scene's spoken words.
		for field, value := range ctx {
			if s, ok := value.(string); ok {
				for _, word := range []string{"Il", "celebre", "incontro", "di", "Teano", "Teano."} {
					assert.NotEqual(t, word, s, "event %s leaked word %q in field %q", e.Message, word, field)
				}
			}
		}
	}
}

func TestTimingObservabilityEvents_Failed(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	log := zap.New(core)

	tts := &stubProcessTTS{cannedOut: TTSOutput{
		LocalPath: "/tmp/vo/no-boundaries.mp3",
		Voice:     "it-IT-DiegoNeural",
		Provider:  "edge_tts",
		Duration:  2 * time.Second,
		// No WordBoundaries → required timing must fail closed.
	}}
	pub := &stubProcessPublisher{fileID: "drive-nb"}
	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: openProcessTestDB(t)},
		Finalizer:           &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "vo-nb"}},
		Logger:              log,
	})

	_, err := uc.Execute(context.Background(), &ProcessSegmentCommand{
		ID:       "vo-nb",
		JobID:    "job-nb",
		Text:     "Nessun boundary.",
		Language: "it",
		Voice:    "it-IT-DiegoNeural",
		Filename: "scene-0.mp3",
		Timing: &audio.TimingRequest{
			Mode:         audio.TimingRequired,
			BoundaryMode: audio.BoundaryWord,
			Formats:      []audio.TimingFormat{audio.TimingJSON},
		},
		Dest: &ResolvedDestination{FolderID: "folder-nb", FolderPath: t.TempDir()},
	})
	require.Error(t, err)

	msgs := timingMessages(observed)
	require.Contains(t, msgs, "voiceover.timing.capture.started")
	require.Contains(t, msgs, "voiceover.timing.capture.completed")
	require.Contains(t, msgs, "voiceover.timing.failed")
	require.NotContains(t, msgs, "voiceover.timing.normalized")
	require.NotContains(t, msgs, "voiceover.timing.published")

	// The failed event still carries scene/language/provider, never words.
	for _, e := range observed.All() {
		if e.Message != "voiceover.timing.failed" {
			continue
		}
		ctx := e.ContextMap()
		assert.Equal(t, "vo-nb", ctx["scene_id"])
		assert.Equal(t, "it", ctx["language"])
		assert.Equal(t, "edge_tts", ctx["provider"])
	}
}
