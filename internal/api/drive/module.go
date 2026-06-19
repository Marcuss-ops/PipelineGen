package drive

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// NewModule creates a new Drive module.
func NewModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *DriveHandler,
) *api.RouteModule {
	return api.NewRouteModule(
		"drive",
		func(cfg *config.Config) bool { return cfg.Features.DriveEnabled },
		"/drive",
		handler,
		log,
	)
}
