package bootstrap

import (
	"go.uber.org/zap"
	"velox/go-master/internal/runtime"
	"velox/go-master/pkg/config"
)

// BackgroundDeps is empty in the minimal server.
type BackgroundDeps struct{}

// initBackgroundServices returns no background services in the stripped-down build.
func initBackgroundServices(
	_ *config.Config, _ *zap.Logger, _ *CoreDeps, _ *ClipDeps, _ *DriveDeps,
) (*BackgroundDeps, []runtime.BackgroundService, error) {
	return &BackgroundDeps{}, nil, nil
}
