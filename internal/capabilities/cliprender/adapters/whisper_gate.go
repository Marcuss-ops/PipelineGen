package adapters

import (
	"context"
	"os"
	"strconv"
	"strings"
)

// whisperGPUConcurrency bounds concurrent Whisper ASR processes in this
// PipelineGen process. Whisper and Chronon share one GPU; before this gate an
// unbounded clip fan-out could start N faster-whisper processes while Chronon
// held its Vulkan allocations, which is the plausible live OOM path (CR-15).
//
// The default is 1: transcription is a short, latency-tolerant step compared
// with a render, and serializing it is the safe side of the VRAM contract. The
// operator can widen it explicitly with VELOX_WHISPER_MAX_CONCURRENCY after
// measuring peak VRAM alongside Chronon. That is the SAME knob the Python bridge
// reads for its host-wide flock slots, so the in-process gate and the host gate
// cannot declare different widths.
var whisperGPUSlots = make(chan struct{}, whisperGPUConcurrency())

func whisperGPUConcurrency() int {
	raw := strings.TrimSpace(os.Getenv("VELOX_WHISPER_MAX_CONCURRENCY"))
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// acquireWhisperGPU waits for a Whisper slot or the caller's context. The
// returned release function is idempotent-safe enough for defer at every call
// site: each successful acquire releases exactly one token.
func acquireWhisperGPU(ctx context.Context) (func(), error) {
	select {
	case whisperGPUSlots <- struct{}{}:
		return func() { <-whisperGPUSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
