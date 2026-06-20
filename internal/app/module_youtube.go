package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	ytsources "github.com/Marcuss-ops/PipelineGen/internal/api/sources/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	ytService "github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"
	"go.uber.org/zap"
)

// YouTubeClipWiring holds the YouTube Clip module wiring.
type YouTubeClipWiring struct {
	Handler *ytsources.YouTubeClipHandler
	Module  module.Module
	Service *ytService.Service
}

// WireYouTubeClip creates the YouTube Clip handler and module.
func WireYouTubeClip(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*YouTubeClipWiring, error) {
	handler := ytsources.NewYouTubeClipHandler(coreDeps.YoutubeClipService, log, coreDeps.JobServiceFacade)
	var mod module.Module
	if coreDeps.YoutubeClipService != nil {
		mod = sources.NewClipsModule(cfg, log, coreDeps.YoutubeClipService, handler, coreDeps.JobServiceFacade)
		log.Info("created Clips module")
		coreDeps.YoutubeClipService.RegisterHandler(coreDeps.JobsService)
	}
	if coreDeps.ClipsRepo != nil {
		handler.SetClipsRepo(coreDeps.ClipsRepo)
	}
	return &YouTubeClipWiring{Handler: handler, Module: mod, Service: coreDeps.YoutubeClipService}, nil
}
