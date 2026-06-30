package app

import (
	"fmt"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	ytsources "github.com/Marcuss-ops/PipelineGen/internal/api/assets/youtube"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	ytService "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)
func WireYouTubeClip(cfg *config.Config, log *zap.Logger, ytSvc *ytService.Service, jobFacade jobdomain.Service, jobs *appjobs.Service, clipsRepo *assets.ClipsRepository, toolChecker appassets.ToolChecker, idempotencyMiddleware gin.HandlerFunc, searchAggregator *providers.SearchAggregator) (*YouTubeClipWiring, error) {
	if ytSvc == nil {
		return nil, nil // tolerated: module is skipped
	}
	descriptor, err := wireYouTubeClipModule(cfg, ytSvc, jobFacade, clipsRepo, toolChecker, idempotencyMiddleware, searchAggregator, log)
	if err != nil {
		return nil, fmt.Errorf("WireYouTubeClip: %w", err)
	}
	yd, typeAssertOk := descriptor.(*ytsources.YouTubeDescriptor)
	if !typeAssertOk || yd == nil {
		return nil, fmt.Errorf("WireYouTubeClip: ytsources.Build returned unexpected descriptor type %T (want *ytsources.YouTubeDescriptor)", descriptor)
	}
	// Canonical late-bind step: route the YouTube service's worker
	// handlers (extraction, channel sync, …) into jobs.Service at
	// composition time. Stays outside the Build contract because
	// today's YouTube Descriptor does NOT register its own job slot
	// (no DescriptorJobs implementation); the registration happens
	// once per process via the canonical service.RegisterHandler(jobs).
	ytSvc.RegisterHandler(jobs)
	return &YouTubeClipWiring{Module: yd.Module, Service: ytSvc}, nil
}

// wireYouTubeClipModule composes the YouTube HTTP module by delegating
// to the canonical `ytsources.Build(deps Dependencies) (api.Descriptor, error)`
// entrypoint. The composition root has the only knowledge of
// `cfg.Features.YouTubeEnabled`; this function maps that onto the
// typed-narrow Dependencies.
//
// Always returns (non-nil descriptor, nil error) when ytSvc is non-nil;
// the caller (WireYouTubeClip) handles the nil-tolerance path before
// reaching this helper.
//
// Note: the `*appjobs.Service` (`jobs` arg of WireYouTubeClip) is
// consumed by the late-bind `ytSvc.RegisterHandler(jobs)` step at the
// end of WireYouTubeClip — it is intentionally NOT threaded through
// this helper because Build does not register any job-handler slot
// (the YouTube Descriptor does not implement DescriptorJobs). Mirrors
// the artlist pattern where `bundle.Jobs.Service.RegisterHandler(artlistSvc.HandleJob)`
// stays at the WireArtlist-service step, NOT inside the Build contract.
func wireYouTubeClipModule(cfg *config.Config, ytSvc *ytService.Service, jobFacade jobdomain.Service, clipsRepo *assets.ClipsRepository, toolChecker appassets.ToolChecker, idempotencyMiddleware gin.HandlerFunc, searchAggregator *providers.SearchAggregator, log *zap.Logger) (api.Descriptor, error) {
	return ytsources.Build(ytsources.Dependencies{
		Service:          ytSvc,
		Jobs:             jobFacade, // NewYouTubeClipHandler accepts jobservice.Service (jobFacade implements it)
		ClipStorePort:    newClipStoreAdapter(clipsRepo),
		ToolChecker:      toolChecker,
		Idempotency:      idempotencyMiddleware,
		SearchAggregator: searchAggregator,
		EnabledFunc:      func() bool { return cfg.Features.YouTubeEnabled },
		ModuleOpts:       nil, // no per-feature middleware for the clips capability (matches pre-Step-4 wiring);
		Logger:           log,
	})
}

// FullImagesWiring holds the FullImages module wiring.
//
// PR3 (June 2026): Wave 14 close. The handler was moved from
// `internal/api/fullimages/` to `internal/api/images/` as a sibling
// of ImagesHandler. The route prefix stays `/fullimages` (NOT
// `/images`) so the public REST URL stays unchanged — zero-change-
// contract per PR3 spec. The sub-path `/video/generate` is unchanged
// (no collision with `ImagesHandler.Generate` which mounts at
