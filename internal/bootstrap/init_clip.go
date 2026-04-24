package bootstrap

import (
	"go.uber.org/zap"
	"velox/go-master/internal/runtime"
	"velox/go-master/pkg/config"
)

// ClipDeps is empty in the stripped-down server.
type ClipDeps struct{}

// initClipSystem does nothing in the stripped-down server.
func initClipSystem(_ *config.Config, _ *zap.Logger, _ *CoreDeps) (*ClipDeps, []runtime.BackgroundService, error) {
	return &ClipDeps{}, nil, nil
}
