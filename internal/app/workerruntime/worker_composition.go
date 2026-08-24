package workerruntime

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// InitWorkerComposition builds the full wiring.ComposeRoot (DB, repos, services,
// Drive, AI, etc.) and runs WireRegistry so that all job handlers are
// registered on the in-process Dispatcher.
func InitWorkerComposition(cfg *config.Config, log *zap.Logger) (*wiring.ComposeRoot, wiring.CleanupFunc, error) {
	root, _, cleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return nil, nil, fmt.Errorf("init worker composition: %w", err)
	}
	_, err = wiring.WireRegistry(context.Background(), cfg, log, root)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("wire registry: %w", err)
	}
	return root, cleanup, nil
}
