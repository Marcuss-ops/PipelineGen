package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	ytsources "github.com/Marcuss-ops/PipelineGen/internal/api/sources/youtube"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	ytService "github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"go.uber.org/zap"
)

// YouTubeClipWiring holds the YouTube Clip module wiring.
type YouTubeClipWiring struct {
	Handler *ytsources.YouTubeClipHandler
	Module  module.Module
	Service *ytService.Service
}

// WireYouTubeClip creates the YouTube Clip handler and module.
//
// PR4d-chunk2 (June 2026): takes 4 direct narrow args (no bundle —
// only 4 cross-bundle reads, no coherence warrant for a bundle).
func WireYouTubeClip(cfg *config.Config, log *zap.Logger, ytSvc *ytService.Service, jobFacade *jobdomain.Service, jobs *appjobs.Service, clipsRepo *assets.ClipsRepository) (*YouTubeClipWiring, error) {
	handler := ytsources.NewYouTubeClipHandler(ytSvc, log, jobFacade)
	var mod module.Module
	if ytSvc != nil {
		mod = sources.NewClipsModule(cfg, log, ytSvc, handler, jobFacade)
		log.Info("created Clips module")
		ytSvc.RegisterHandler(jobs)
	}
	if clipsRepo != nil {
		handler.SetClipsRepo(clipsRepo)
	}
	return &YouTubeClipWiring{Handler: handler, Module: mod, Service: ytSvc}, nil
}
