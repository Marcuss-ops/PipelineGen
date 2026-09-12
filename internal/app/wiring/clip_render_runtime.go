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
	// Completion is event-driven (the queue client long-polls
	// GET /jobs/{id}/wait), so this only tunes the polling FALLBACK used when
	// the queue server lacks that route. The configured value (default 0 →
	// the built-in 250 ms) is applied before the runtime is cached in the
	// composition root.
	if cfg.External.RenderingGenPollIntervalMS > 0 {
		executor.SetPollInterval(time.Duration(cfg.External.RenderingGenPollIntervalMS) * time.Millisecond)
	}

	// The async wrapper is composition-owned and opt-in. When disabled this is
	// literally the historical executor. When enabled it exposes the SAME
	// executor plus durable CAS/enqueue dependencies through
	// cliprender.AsyncCompletionProvider; Worker.WithRenderExecutor discovers
	// that capability without a second registry or a second backend selector.
	var renderExecutor cliprender.RenderExecutor = executor
	renderExecutor, err = wrapClipRenderAsyncCompletion(cfg, root, renderExecutor, log)
	if err != nil {
		return nil, err
	}

	runtime := &ClipRenderRuntime{RenderingGenExecutor: renderExecutor}
	root.ClipRenderRuntime = runtime
	return runtime, nil
}
