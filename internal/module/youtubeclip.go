package module

import (
	"context"

	youtubecliphandler "velox/go-master/internal/api/handlers/youtubeclip"
	"velox/go-master/internal/api/middleware"
	jobservice "velox/go-master/internal/service/jobs"
	youtubeclip "velox/go-master/internal/service/youtubeclip"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// NewYouTubeClipModule creates a new YouTubeClip module using RouteModule
func NewYouTubeClipModule(
	cfg *config.Config,
	log *zap.Logger,
	service *youtubeclip.Service,
	handler *youtubecliphandler.Handler,
	jobsSvc *jobservice.Service,
) *RouteModule {
	return NewRouteModule(
		"youtube-clips",
		func(cfg *config.Config) bool { return cfg.Features.YouTubeEnabled },
		"/youtube-clips",
		handler,
		log,
		WithStart(func(ctx context.Context) error {
			log.Info("starting youtube clips module")
			if service != nil {
				service.RegisterHandler(jobsSvc)
			}
			return nil
		}),
		WithStop(func(ctx context.Context) error {
			log.Info("stopping youtube clips module")
			return nil
		}),
		WithMiddleware(middleware.YouTubeEnabled(cfg)),
	)
}
