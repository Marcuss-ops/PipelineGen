package voiceover

import (
	"strings"
	"testing"
	"time"

	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	obs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestTTSSlotIsGrantedOncePerUnitAcrossChunks certifies WST-07: the TTS gate
// admits a UNIT (one scene × language), not each sentence chunk of it. Before
// the wrapper swap the same unit acquired the slot once per 24-word chunk —
// 20.034 acquisitions for 4.919 synthesize calls in the audit window, 4,57 h of
// `semaphore_wait/tts` — so a scene paid the queue as many times as it had
// sentences.
func TestTTSSlotIsGrantedOncePerUnitAcrossChunks(t *testing.T) {
	provider := &recordingTTS{}
	merger := &recordingMerger{}
	chunker := &chunkedTTSProvider{inner: provider, merger: merger, concurrency: 4}
	gate := NewRateLimitedTTSProvider(chunker, config.VoiceoverConcurrencyConfig{
		MaxConcurrentTTS: 4,
		TTSTimeoutSec:    30,
	}, zap.NewNop())

	ctx, run := boundFinalAudioRun(t, "run-tts-unit-admission")
	text := strings.Repeat("parola ", 96) // 4 sentence-sized chunks of 24 words
	_, err := gate.Synthesize(ctx, voiceover.TTSInput{
		Text: text, Filename: "final.mp3", OutputDir: t.TempDir(), Language: "it",
	})
	require.NoError(t, err)
	require.Equal(t, 4, provider.calls, "the unit still fans out one request per chunk")

	waits := 0
	for _, wait := range run.Report().Waits {
		if wait.Kind == obs.WaitSemaphore && wait.Component == string(obs.ComponentTTS) {
			waits++
		}
	}
	require.Equal(t, 1, waits, "the unit must pay the TTS queue once, not once per chunk")
}

// TestTTSSlotIsNotRequeuedWhenTheGateIsContented pins the same contract under
// contention, which is where the old chain burned the queue: the unit waits
// ONCE even when every slot is held, instead of re-queuing per chunk.
func TestTTSSlotIsNotRequeuedWhenTheGateIsContented(t *testing.T) {
	provider := &recordingTTS{}
	merger := &recordingMerger{}
	chunker := &chunkedTTSProvider{inner: provider, merger: merger, concurrency: 4}
	// Capacity 1 makes the contention deterministic.
	gate := NewRateLimitedTTSProvider(chunker, config.VoiceoverConcurrencyConfig{
		MaxConcurrentTTS: 1,
		TTSTimeoutSec:    30,
	}, zap.NewNop())

	ctx, run := boundFinalAudioRun(t, "run-tts-unit-contended")
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _ = gate.Synthesize(ctx, voiceover.TTSInput{
			Text: strings.Repeat("parola ", 96), Filename: "first.mp3", OutputDir: t.TempDir(), Language: "it",
		})
	}()
	time.Sleep(10 * time.Millisecond) // let the first unit take the only slot
	_, err := gate.Synthesize(ctx, voiceover.TTSInput{
		Text: strings.Repeat("parola ", 96), Filename: "second.mp3", OutputDir: t.TempDir(), Language: "it",
	})
	require.NoError(t, err)
	<-firstDone

	waits := 0
	for _, wait := range run.Report().Waits {
		if wait.Kind == obs.WaitSemaphore && wait.Component == string(obs.ComponentTTS) {
			waits++
		}
	}
	require.LessOrEqual(t, waits, 2, "at most one queue wait per unit")
	require.Equal(t, 8, provider.calls, "both units still fan out four chunks each")
}
