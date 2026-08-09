package render

import (
	"fmt"
	"os/exec"
	"strings"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"go.uber.org/zap"
)

// NewConfiguredCutter selects the media execution backend at the composition
// root. The default remains Go until Rust passes the production parity gate;
// unknown or unavailable backends fail closed.
func NewConfiguredCutter(mode, rustBinary, ffmpegPath, encoder string, log *zap.Logger) (stockpipeline.VideoCutter, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "go":
		return NewFFmpegCutterWithEncoder(ffmpegPath, encoder, log), nil
	case "rust":
		if strings.TrimSpace(rustBinary) == "" {
			return nil, fmt.Errorf("rust media executor requires rust_muscles_path")
		}
		if _, err := exec.LookPath(rustBinary); err != nil {
			return nil, fmt.Errorf("rust media executor %q is unavailable: %w", rustBinary, err)
		}
		return NewRustCutter(rustBinary, ffmpegPath, log), nil
	default:
		return nil, fmt.Errorf("unsupported media executor %q", mode)
	}
}
