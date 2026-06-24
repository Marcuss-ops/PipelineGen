package app

import (
	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	ytsources "github.com/Marcuss-ops/PipelineGen/internal/api/assets/youtube"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
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
	Module  api.Module
	Service *ytService.Service
}

// WireYouTubeClip creates the YouTube Clip handler and module.
//
// PR4d-chunk2 (June 2026): takes 4 direct narrow args (no bundle —
// only 4 cross-bundle reads, no coherence warrant for a bundle).
// PR3 (June 2026): providerRegistry added for constructor injection
// (replaces post-construction SetProviderRegistry).
// PG-003 (June 2026): clipsRepo (still typed *assets.ClipsRepository
// at the wiring seam) is passed through the canonical
// newClipStoreAdapter(...) helper defined in youtube_adapters.go. The
// handler depends on the typed youtubeports.ClipStorePort only; the
// helper itself preserves `if h.clipsRepo != nil` semantics in the
// handler because newClipStoreAdapter(nil) returns a nil interface.
func WireYouTubeClip(cfg *config.Config, log *zap.Logger, ytSvc *ytService.Service, jobFacade jobdomain.Service, jobs *appjobs.Service, clipsRepo *assets.ClipsRepository, providerRegistry *providers.Registry, toolChecker appassets.ToolChecker) (*YouTubeClipWiring, error) {
	handler := ytsources.NewYouTubeClipHandler(ytSvc, log, jobFacade, providerRegistry, newClipStoreAdapter(clipsRepo), toolChecker)
	var mod api.Module
	if ytSvc != nil {
		mod = api.NewRouteModule(
			"clips",
			func() bool { return cfg.Features.YouTubeEnabled },
			"/clips",
			handler,
			log,
		)
		log.Info("created Clips module")
		ytSvc.RegisterHandler(jobs)
	}
	return &YouTubeClipWiring{Handler: handler, Module: mod, Service: ytSvc}, nil
}
