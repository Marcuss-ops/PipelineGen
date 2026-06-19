package app

import (
	sources "github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"

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
		mod = sources.NewClipsModule(cfg, log, coreDeps.YoutubeClipService, handler, coreDeps.JobsService)
		log.Info("created Clips module")

		// Register job handler for youtube_clip.extract jobs
		coreDeps.YoutubeClipService.RegisterHandler(coreDeps.JobsService)
	}

	// Wire clips repo for advanced search
	if coreDeps.ClipsRepo != nil {
		handler.SetClipsRepo(coreDeps.ClipsRepo)
	}

	return &YouTubeClipWiring{
		Handler: handler,
		Module:  mod,
		Service: coreDeps.YoutubeClipService,
	}, nil
}
