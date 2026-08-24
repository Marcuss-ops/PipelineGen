// Package voiceover — silence_cleanup_event_test.go.
//
// Certifies the silence-cleanup report reaches the two surfaces the
// canonical VO contract requires: the structured voiceover.silence_cleanup
// observability event and the persisted voiceover metadata row. Both carry
// the same four summary durations (original, leading trim, trailing trim,
// clean) so operators can verify the timeline used the cleaned duration.
package voiceover

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func TestSilenceCleanupReportEmittedAsEventAndPersistedInMetadata(t *testing.T) {
	db := openProcessTestDB(t)
	core, observed := observer.New(zap.InfoLevel)
	log := zap.New(core)

	tts := &stubProcessTTS{cannedOut: TTSOutput{
		LocalPath:     "/tmp/vo/scene-0.mp3",
		Voice:         "it-IT-DiegoNeural",
		LegacyFileMD5: "abc123",
		Duration:      45*time.Second + 210*time.Millisecond, // 45_210_000 us pre-clean
	}}
	audioPost := &stubProcessAudioPost{cannedOut: AudioPostOutput{
		CleanedPath: "/tmp/vo/cleaned_scene-0.mp3",
		DurationUS:  43_870_000,
		EditMap: []audio.AudioEdit{
			{SourceStartUS: 0, SourceEndUS: 620_000},
			{SourceStartUS: 44_490_000, SourceEndUS: 45_210_000},
		},
	}}
	pub := &stubProcessPublisher{fileID: "drive-sc"}
	finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "vo-sc"}}

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		AudioPostProcessor:  audioPost,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              log,
	})

	out, err := uc.Execute(context.Background(), &ProcessSegmentCommand{
		ID:            "vo-sc",
		JobID:         "job-sc",
		Text:          "Voce di test.",
		Language:      "it",
		Voice:         "it-IT-DiegoNeural",
		Filename:      "scene-0.mp3",
		RemoveSilence: true,
		Dest:          &ResolvedDestination{FolderID: "folder-sc", FolderPath: t.TempDir()},
	})
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, out.Status)
	require.NotNil(t, out.SilenceCleanup)

	// 1) Structured observability event with the four summary durations.
	var event *observer.LoggedEntry
	for _, e := range observed.All() {
		if e.Message == "voiceover.silence_cleanup" {
			event = &e
			break
		}
	}
	require.NotNil(t, event, "voiceover.silence_cleanup event must be emitted")
	ctx := event.ContextMap()
	assert.Equal(t, "vo-sc", ctx["scene_id"])
	assert.Equal(t, "it", ctx["language"])
	assert.Equal(t, int64(45_210_000), ctx["original_duration_us"])
	assert.Equal(t, int64(620_000), ctx["trim_start_us"])
	assert.Equal(t, int64(720_000), ctx["trim_end_us"])
	assert.Equal(t, int64(43_870_000), ctx["clean_duration_us"])

	// 2) The same report is persisted in the voiceover metadata row.
	require.Len(t, finalizer.calls, 1, "finalizer must be invoked once")
	var meta map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(finalizer.calls[0].MetaJSON, &meta))
	raw, ok := meta["silence_cleanup"]
	require.True(t, ok, "silence_cleanup must be persisted in metadata")
	var report SilenceCleanupReport
	require.NoError(t, json.Unmarshal(raw, &report))
	assert.Equal(t, int64(45_210_000), report.OriginalDurationUS)
	assert.Equal(t, int64(620_000), report.TrimStartUS)
	assert.Equal(t, int64(720_000), report.TrimEndUS)
	assert.Equal(t, int64(43_870_000), report.CleanDurationUS)
}
