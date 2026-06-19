package sources

import (
	"context"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	artlistService "github.com/Marcuss-ops/PipelineGen/internal/sources/artlist"

	"go.uber.org/zap"
)

// NewArtlistModule creates a new Artlist module using RouteModule
func NewArtlistModule(
	cfg *config.Config,
	log *zap.Logger,
	service *artlistService.Service,
	handler *ArtlistHandler,
) *api.RouteModule {
	return api.NewRouteModule(
		"artlist",
		func(cfg *config.Config) bool { return cfg.Features.ArtlistEnabled },
		"/artlist",
		handler,
		log,
		api.WithStart(func(ctx context.Context) error {
			log.Info("starting artlist module")
			return nil
		}),
		api.WithStop(func(ctx context.Context) error {
			log.Info("stopping artlist module")
			if service != nil {
				return service.Close()
			}
			return nil
		}),
		api.WithMiddleware(middleware.ArtlistEnabled(cfg)),
	)
}
