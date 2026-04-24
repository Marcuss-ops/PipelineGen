package bootstrap

import (
	"go.uber.org/zap"
	"velox/go-master/internal/runtime"
	"velox/go-master/pkg/config"
)

// PipelineDeps is empty in the stripped-down server.
type PipelineDeps struct{}

// initPipeline does nothing in the stripped-down server.
func initPipeline(
	_ *config.Config, _ *zap.Logger, _ *CoreDeps, _ *ClipDeps, _ *DriveDeps,
) (*PipelineDeps, []runtime.BackgroundService, CleanupFunc, error) {
	return &PipelineDeps{}, nil, nil, nil
}
