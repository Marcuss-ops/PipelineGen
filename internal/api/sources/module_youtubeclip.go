package sources

import (
	"context"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"

	"go.uber.org/zap"
)

// NewClipsModule creates the canonical clips module for YouTube download and processing.
func NewClipsModule(
	cfg *config.Config,
	log *zap.Logger,
	service *youtube.Service,
	handler *YouTubeClipHandler,
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
