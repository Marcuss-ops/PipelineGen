package module

import (
	drivehandler "velox/go-master/internal/api/handlers/drive"
	"velox/go-master/pkg/config"
	"go.uber.org/zap"
)

// NewDriveModule creates a new Drive module using RouteModule.
func NewDriveModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *drivehandler.Handler,
) *RouteModule {
	return NewRouteModule(
		"drive",
		func(cfg *config.Config) bool { return cfg.Features.DriveEnabled },
		"/drive",
		handler,
		log,
	)
}
