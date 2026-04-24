package bootstrap

import (
	"go.uber.org/zap"
	"velox/go-master/internal/runtime"
	"velox/go-master/pkg/config"
)

// SyncDeps is empty in the stripped-down server.
type SyncDeps struct{}

// initSyncServices is disabled in the stripped-down server.
func initSyncServices(
	_ *config.Config, _ *zap.Logger, _ *ClipDeps, _ *DriveDeps,
) (*SyncDeps, []runtime.BackgroundService, error) {
	return &SyncDeps{}, nil, nil
}
