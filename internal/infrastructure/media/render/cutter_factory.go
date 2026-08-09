package render

import (
	"fmt"
	"os/exec"
	"strings"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// NewConfiguredCutter selects the media execution backend at the composition
// root. The Rust client is the single protocol adapter for every capability.
func NewConfiguredCutter(mode, rustBinary, ffmpegPath string, policy config.VideoEncoderPolicy, profile config.CanonicalVideoProfile, log *zap.Logger) (stockpipeline.VideoCutter, error) {
	return NewConfiguredCutterWithExecutor(mode, rustBinary, ffmpegPath, policy, profile, log, nil)
}

// NewConfiguredCutterWithExecutor uses the composition root's shared Rust
// Executor when supplied, keeping cutter, renderer, and probe under one
// process/cancellation/limiter policy.
func NewConfiguredCutterWithExecutor(mode, rustBinary, ffmpegPath string, policy config.VideoEncoderPolicy, profile config.CanonicalVideoProfile, log *zap.Logger, executor *rustexec.Executor) (stockpipeline.VideoCutter, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "rust":
		if executor == nil {
			if strings.TrimSpace(rustBinary) == "" {
				return nil, fmt.Errorf("rust media executor requires rust_muscles_path")
			}
			if _, err := exec.LookPath(rustBinary); err != nil {
				return nil, fmt.Errorf("rust media executor %q is unavailable: %w", rustBinary, err)
			}
			executor = rustexec.NewExecutor(rustBinary, ffmpegPath, log)
		}
		return rustexec.NewConfiguredVideoProcessorWithExecutor(executor, policy, profile, log), nil
	default:
		return nil, fmt.Errorf("unsupported media executor %q", mode)
	}
}
