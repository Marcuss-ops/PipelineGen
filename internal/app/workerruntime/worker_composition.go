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
// registered on the in-process Dispatcher. It does NOT start background
// jobs, the HTTP server, or the in-process job runner — it is intended
// for the remote worker binary that claims jobs via the HTTP broker and
// executes them using the local service graph.
//
// The caller must invoke BuildWorkerRegistry(root) afterwards to copy the
// handlers into the remote worker.Registry and derive capabilities.
func InitWorkerComposition(cfg *config.Config, log *zap.Logger) (*wiring.ComposeRoot, wiring.CleanupFunc, error) {
	if err := initLinguistics(cfg, log); err != nil {
		return nil, nil, fmt.Errorf("init worker composition: %w", err)
	}

	ctx := context.Background()

	dbs, err := wiring.InitDatabases(ctx, cfg, log)
	if err != nil {
		return nil, nil, fmt.Errorf("init wiring.Databases: %w", err)
	}
	cleanup := func() {
		dbs.Close()
	}

	if err := wiring.RunAllMigrations(dbs, log); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("run migrations: %w", err)
	}

	root, err := NewComposition(ctx, cfg, dbs, log)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("new composition: %w", err)
	}

	// WireRegistry registers all job handlers on root.Jobs.Dispatcher.
	// We ignore the returned wiring because the worker does not mount HTTP modules.
	if _, err := WireRegistry(ctx, cfg, log, root); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("wire registry: %w", err)
	}

	return root, cleanup, nil
}
