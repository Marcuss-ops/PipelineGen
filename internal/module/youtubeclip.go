package module

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/api/handlers/sources"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"

	"go.uber.org/zap"
)

// NewClipsModule creates the canonical clips module for YouTube download and processing.
func NewClipsModule(
	cfg *config.Config,
	log *zap.Logger,
	service *youtube.Service,
	handler *sources.YouTubeClipHandler,
	jobsSvc *jobservice.Service,
) *RouteModule {
	return NewRouteModule(
		"clips",
		func(cfg *config.Config) bool { return cfg.Features.YouTubeEnabled },
		"/clips",
		handler,
		log,
		WithStart(func(ctx context.Context) error {
			log.Info("starting clips module")
			return nil
		}),
		WithStop(func(ctx context.Context) error {
			log.Info("stopping clips module")
			return nil
		}),
	)
}
