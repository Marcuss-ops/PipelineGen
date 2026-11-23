package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"go.uber.org/zap"
)

// TestWhisperGPUConcurrencyUsesHostKnob pins the single knob: the in-process
// gate and the Python host-wide flock must read the same environment variable,
// otherwise an operator who widens one silently keeps the other serialized.
func TestWhisperGPUConcurrencyUsesHostKnob(t *testing.T) {
	t.Setenv("VELOX_WHISPER_MAX_CONCURRENCY", "3")
	if got := whisperGPUConcurrency(); got != 3 {
		t.Fatalf("whisperGPUConcurrency = %d, want 3 from VELOX_WHISPER_MAX_CONCURRENCY", got)
	}
	t.Setenv("VELOX_WHISPER_MAX_CONCURRENCY", "0")
	if got := whisperGPUConcurrency(); got != 1 {
		t.Fatalf("whisperGPUConcurrency = %d, want the safe default 1", got)
	}
}

func TestTranscribeStreamWaitsForWhisperGPUSlot(t *testing.T) {
	// Fill every process-wide slot, then prove the streaming path waits for
	// one instead of starting another ASR process (CR-15).
	for i := 0; i < cap(whisperGPUSlots); i++ {
		whisperGPUSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(whisperGPUSlots); i++ {
			<-whisperGPUSlots
		}
	}()

	transcriber := &ClipRenderStreamingTranscriber{log: zap.NewNop()}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := transcriber.TranscribeStream(ctx, &cliprender.MaterializedAsset{
		AssetID: "asset-whisper-gate", LocalPath: "/tmp/source.mp4", SHA256: "abc",
	}, "en")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline while every Whisper slot is held", err)
	}
}
