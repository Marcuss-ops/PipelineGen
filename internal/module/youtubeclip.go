package module

import (
	"context"

	"velox/go-master/internal/api/handlers/sources"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// NewYouTubeClipModule creates a new YouTubeClip module using RouteModule
func NewYouTubeClipModule(
	cfg *config.Config,
	log *zap.Logger,
	service *youtubeclip.Service,
	handler *sources.YouTubeClipHandler,
	jobsSvc *jobservice.Service,
) *RouteModule {
	return NewRouteModule(
		"youtube-clips",
		func(cfg *config.Config) bool { return cfg.Features.YouTubeEnabled },
		"/youtube-clips",
		handler,
		log,
		WithStart(func(ctx context.Context) error {
			log.Info("starting youtube-clips module")
			return nil
		}),
		WithStop(func(ctx context.Context) error {
			log.Info("stopping youtube-clips module")
			return nil
		}),
	)
}
