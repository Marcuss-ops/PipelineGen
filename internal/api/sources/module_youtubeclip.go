package sources

import (
	"context"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	ytsources "github.com/Marcuss-ops/PipelineGen/internal/api/sources/youtube"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	domainyoutube "github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"

	"go.uber.org/zap"
)

// NewClipsModule creates the canonical clips module for YouTube download
// and processing. The YouTubeClipHandler now lives in the youtube
// subpackage (PR-A Phase 2) so this module factory takes the new type
// directly.
func NewClipsModule(
	cfg *config.Config,
	log *zap.Logger,
	service *domainyoutube.Service,
	handler *ytsources.YouTubeClipHandler,
	jobsSvc *jobservice.Service,
) *api.RouteModule {
	return api.NewRouteModule(
		"clips",
		func(cfg *config.Config) bool { return cfg.Features.YouTubeEnabled },
		"/clips",
		handler,
		log,
		api.WithStart(func(ctx context.Context) error {
			log.Info("starting clips module")
			return nil
		}),
		api.WithStop(func(ctx context.Context) error {
			log.Info("stopping clips module")
			return nil
		}),
	)
}
