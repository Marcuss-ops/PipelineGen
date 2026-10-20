package render

import (
	"fmt"
	"os/exec"
	"strings"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"go.uber.org/zap"
)

// NewConfiguredCutter selects the media execution backend at the composition
// root. The Rust client is the single protocol adapter for every capability.

// NewConfiguredCutterWithExecutor uses the composition root's shared Rust
// Executor when supplied, keeping cutter, renderer, and probe under one
// process/cancellation/limiter policy.

func NewConfiguredCutterWithExecutor(rustBinary, ffmpegPath string, policy mediaexec.EncoderPolicy, profile mediaexec.VideoProfile, log *zap.Logger, executor *rustexec.Executor) (stockpipeline.VideoCutter, error) {
	return NewConfiguredCutterWithExecutionConfig(rustBinary, ffmpegPath, mediaexec.ExecutionConfig{Profile: profile, Policy: policy}, log, executor)
}

// NewConfiguredCutterWithExecutionConfig wires the full Go-owned media
// execution contract — policy, profile, and the cut capacity policy
// (MaxParallelCuts) — into the Rust cutter backend.
func NewConfiguredCutterWithExecutionConfig(rustBinary, ffmpegPath string, execCfg mediaexec.ExecutionConfig, log *zap.Logger, executor *rustexec.Executor) (stockpipeline.VideoCutter, error) {
	if executor == nil {
		if strings.TrimSpace(rustBinary) == "" {
			return nil, fmt.Errorf("rust media executor requires rust_muscles_path")
		}
		if _, err := exec.LookPath(rustBinary); err != nil {
			return nil, fmt.Errorf("rust media executor %q is unavailable: %w", rustBinary, err)
		}
		executor = rustexec.NewExecutor(rustBinary, ffmpegPath, log)
	}
	return rustexec.NewConfiguredVideoProcessorWithExecutionConfig(executor, execCfg, log), nil
}
