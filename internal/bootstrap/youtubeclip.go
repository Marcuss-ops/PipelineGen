package bootstrap

import (
	youtubecliphandler "velox/go-master/internal/api/handlers/youtubeclip"
	"velox/go-master/internal/module"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// YouTubeClipWiring holds the YouTube Clip module wiring
type YouTubeClipWiring struct {
	Handler *youtubecliphandler.Handler
	Module  module.Module
}

// WireYouTubeClip creates the YouTube Clip handler and module
func WireYouTubeClip(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*YouTubeClipWiring, error) {
	handler := youtubecliphandler.NewHandler(coreDeps.YoutubeClipService, log, coreDeps.JobsService)

	var mod module.Module
	if coreDeps.YoutubeClipService != nil {
		mod = module.NewYouTubeClipModule(cfg, log, coreDeps.YoutubeClipService, handler, coreDeps.JobsService)
		log.Info("created YouTube Clips module")

		// Register job handler for youtube_clip.extract jobs
		coreDeps.YoutubeClipService.RegisterHandler(coreDeps.JobsService)
	}

	return &YouTubeClipWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
