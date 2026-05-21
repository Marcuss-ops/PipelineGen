package app

import (
	"velox/go-master/internal/api/handlers/sources"
	"velox/go-master/internal/config"
	"velox/go-master/internal/module"
	"velox/go-master/internal/sources/youtube"

	"go.uber.org/zap"
)

// YouTubeClipWiring holds the YouTube Clip module wiring
type YouTubeClipWiring struct {
	Handler *sources.YouTubeClipHandler
	Module  module.Module
	Service *youtube.Service
}

// WireYouTubeClip creates the YouTube Clip handler and module
func WireYouTubeClip(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*YouTubeClipWiring, error) {
	handler := sources.NewYouTubeClipHandler(coreDeps.YoutubeClipService, log, coreDeps.JobsService)

	var mod module.Module
	if coreDeps.YoutubeClipService != nil {
		mod = module.NewClipsModule(cfg, log, coreDeps.YoutubeClipService, handler, coreDeps.JobsService)
		log.Info("created Clips module")

		// Register job handler for youtube_clip.extract jobs
		coreDeps.YoutubeClipService.RegisterHandler(coreDeps.JobsService)
	}

	return &YouTubeClipWiring{
		Handler: handler,
		Module:  mod,
		Service: coreDeps.YoutubeClipService,
	}, nil
}
