package sources

import (
	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	artsources "github.com/Marcuss-ops/PipelineGen/internal/api/sources/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"

	"go.uber.org/zap"
)

// NewArtlistModule creates a new Artlist module using RouteModule.
// The ArtlistHandler now lives in the artlist subpackage (PR-A Phase 3)
// so this module factory takes the new subpackage type.
//
// Lifecycle: the module itself is route-only. The service's Close() is
// now managed by the composition root (internal/app).
func NewArtlistModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *artsources.ArtlistHandler,
) *api.RouteModule {
	return api.NewRouteModule(
		"artlist",
		func() bool { return cfg.Features.ArtlistEnabled },
		"/artlist",
		handler,
		log,
		api.WithMiddleware(middleware.ArtlistEnabled(cfg)),
	)
}
