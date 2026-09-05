package wiring

import (
	"fmt"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	"go.uber.org/zap"
)

// ClipRenderRuntime is the production clip-render graph. RenderingGen is the
// only executor; it owns semantic lowering, queue execution, and Chronon.
type ClipRenderRuntime struct {
	RenderingGenExecutor cliprender.RenderExecutor
}

func BuildClipRenderRuntime(cfg *config.Config, root *ComposeRoot, log *zap.Logger) (*ClipRenderRuntime, error) {
	if root == nil {
		return nil, fmt.Errorf("clip render runtime: composition root is nil")
	}
	if root.ClipRenderRuntime != nil {
		return root.ClipRenderRuntime, nil
	}
	if cfg == nil {
		return nil, fmt.Errorf("clip render runtime: config is required")
	}
	if root.MediaExec == (mediaexec.ExecutionConfig{}) {
		return nil, fmt.Errorf("clip render runtime: resolved media execution config is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	queueURL := strings.TrimSpace(cfg.External.RenderingGenQueueURL)
	if queueURL == "" {
		return nil, fmt.Errorf("clip render runtime: RENDERINGGEN_QUEUE_URL is required; clip rendering fails closed")
	}
	executor, err := renderinggen.NewClipRenderExecutor(renderinggen.New(queueURL))
	if err != nil {
		return nil, fmt.Errorf("clip render runtime: build RenderingGen executor: %w", err)
	}
	// Tighten the queue poll cadence when configured. Each poll tick is
	// pure dead time on a finished job, so the configured value (default 0
	// → the executor's built-in 2 s) is applied before the runtime is
	// cached in the composition root.
	if cfg.External.RenderingGenPollIntervalMS > 0 {
		executor.SetPollInterval(time.Duration(cfg.External.RenderingGenPollIntervalMS) * time.Millisecond)
	}
	runtime := &ClipRenderRuntime{RenderingGenExecutor: executor}
	root.ClipRenderRuntime = runtime
	return runtime, nil
}
