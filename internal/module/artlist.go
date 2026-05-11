package module

import (
	"context"

	"velox/go-master/internal/api/handlers/sources"
	"velox/go-master/internal/api/middleware"
	artlistService "velox/go-master/internal/service/artlist"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// NewArtlistModule creates a new Artlist module using RouteModule
func NewArtlistModule(
	cfg *config.Config,
	log *zap.Logger,
	service *artlistService.Service,
	handler *sources.ArtlistHandler,
) *RouteModule {
	return NewRouteModule(
		"artlist",
		func(cfg *config.Config) bool { return cfg.Features.ArtlistEnabled },
		"/artlist",
		handler,
		log,
		WithStart(func(ctx context.Context) error {
			log.Info("starting artlist module")
			return nil
		}),
		WithStop(func(ctx context.Context) error {
			log.Info("stopping artlist module")
			if service != nil {
				return service.Close()
			}
			return nil
		}),
		WithMiddleware(middleware.ArtlistEnabled(cfg)),
	)
}
