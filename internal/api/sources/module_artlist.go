package sources

import (
	"context"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	artsources "github.com/Marcuss-ops/PipelineGen/internal/api/sources/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	artlistService "github.com/Marcuss-ops/PipelineGen/internal/application/artlist"

	"go.uber.org/zap"
)

// NewArtlistModule creates a new Artlist module using RouteModule.
// The ArtlistHandler now lives in the artlist subpackage (PR-A Phase 3)
// so this module factory takes the new subpackage type.
func NewArtlistModule(
	cfg *config.Config,
	log *zap.Logger,
	service *artlistService.Service,
	handler *artsources.ArtlistHandler,
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
