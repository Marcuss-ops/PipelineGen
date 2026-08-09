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
func NewConfiguredCutter(mode, rustBinary, ffmpegPath string, policy config.VideoEncoderPolicy, log *zap.Logger) (stockpipeline.VideoCutter, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "rust":
		if strings.TrimSpace(rustBinary) == "" {
			return nil, fmt.Errorf("rust media executor requires rust_muscles_path")
		}
		if _, err := exec.LookPath(rustBinary); err != nil {
			return nil, fmt.Errorf("rust media executor %q is unavailable: %w", rustBinary, err)
		}
		return rustexec.NewConfiguredVideoProcessor(rustBinary, ffmpegPath, policy, log), nil
	default:
		return nil, fmt.Errorf("unsupported media executor %q", mode)
	}
}
