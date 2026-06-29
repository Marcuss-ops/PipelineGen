// Package app — public route-module registrations (PR4 split).
//
// PR4 mechanical split (June 2026): relocated from registry.go without
// signature or behaviour changes. Each function in this file registers
// a thin route module that exposes an /api/* surface. Bundle-driven
// modules (Artlist, YouTubeClip, MediaIngest, Scraper, FullImages,
// StockPipeline) live in registry_internal_modules.go instead —
// those have cross-step wiring dependencies that need a different
// ordering surface.
//
// Failures propagate to the orchestrator with the original
// "wire registry: <capability>: %w" wrapping. The PR4 spec mandates
// preservation of those exact error strings; tests in
// internal/app/registry_*.go match against them.
package app

import (
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	imagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/images"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/searchqueries"
	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/application/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	sqliteassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// registerSystem wires the system module.
// PR3 (June 2026): Wave 14 close — the system module absorbed the
// former `internal/api/drive/` directory as a second receiver
// (DriveHandler) sharing the same /drive sub-group. The ctor takes
// driveUploader + reconcileSvc so /drive routes can answer (when
// either is nil the corresponding handler returns 503).
func registerSystem(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot) error {
	var driveUploaderAdapter *drive.Uploader
	if root.Drive != nil && root.Drive.DriveClient != nil {
		driveUploaderAdapter = &drive.Uploader{Service: root.Drive.DriveClient, Log: log}
	}
	return tryRegisterModuleStrict(registry, log, systemapi.NewModule(
		doctorConfigFrom(cfg),
		log,
		toolCheckerAdapter, processRunnerAdapter, dbHealthCheckerAdapter,
		newDriveAdminAdapter(driveUploaderAdapter, log),
		&noopReconciler{},
	), WithRegistrationPoint("register.System"))
}

// registerJobs wires the /jobs route module. PR0 (June 2026): signature
// is now (job.Service, JobStatsReader, *zap.Logger). *root.Jobs.Service
// satisfies both interfaces — it implements the canonical domain
// job.Service (orchestrator) AND the JobStatsReader port (via the
// runtime type-assertion GetStats helper).
func registerJobs(registry *module.Registry, log *zap.Logger, root *ComposeRoot) error {
	jobsDescriptor, err := jobsapi.Build(jobsapi.Dependencies{
		Service:     root.Jobs.Service,
		Stats:       root.Jobs.Service, // *appjobs.Service satisfies both domainjob.Service + appjobs.JobStatsReader
		EnabledFunc: func() bool { return true }, // jobs is always on in production
		ModuleOpts:  nil,                          // no per-feature middleware (matches pre-Step-13 wiring)
		Logger:      log,
	})
	if err != nil {
		return fmt.Errorf("wire registry: jobs: %w", err)
	}
	jd, ok := jobsDescriptor.(*jobsapi.JobsDescriptor)
	if !ok || jd == nil {
		return fmt.Errorf("wire registry: jobs: jobs.Build returned unexpected descriptor type %T (want *jobsapi.JobsDescriptor)", jobsDescriptor)
	}
	log.Info("created Jobs module")
	return tryRegisterModuleStrict(registry, log, jd, WithRegistrationPoint("register.Jobs"))
}

// registerImages wires the /images route module. Consumes
// wiring.MediaIngest.Service for upstream service injection.
//
// PR8 (June 2026): idemHandler installed on POST /api/media/ingest.
func registerImages(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, wiring *RegistryWiring) error {
	var ingestSvc *ingest.Service
	if wiring.MediaIngest != nil {
		ingestSvc = wiring.MediaIngest.Service
	}
	imagesHandler := imagesapi.NewImagesHandler(root.Domains.ImageService, ingestSvc, root.Jobs.Service)
	imagesMod := module.NewRouteModule(
		"images",
		func() bool { return cfg.Features.ImagesEnabled },
		"/images",
		imagesHandler,
		log,
	)
	log.Info("created Images module")
	if err := tryRegisterModuleStrict(registry, log, imagesMod, WithRegistrationPoint("register.Images")); err != nil {
		return fmt.Errorf("wire registry: images: %w", err)
	}
	return nil
}

// registerScriptHistory wires the /scripts history module.
func registerScriptHistory(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot) error {
	if root.Repos == nil || root.Repos.ScriptsRepo == nil {
		return nil
	}
	// NewScriptHistoryModule expects two gin.HandlerFunc gate args
	// (handler feature gate + enabled bool). The helper in
	// internal/api/middleware reads the resolved boolean and wraps
	// it in a 403-on-disabled middleware. Script history is shared by
	// all script entrypoints, so we keep it alive whenever any script
	// feature is enabled.
	scriptHistoryEnabled := anyScriptFeatureEnabled(cfg)
	if err := tryRegisterModuleStrict(registry, log, scriptapi.NewScriptHistoryModule(
		scriptapi.NewScriptHistoryHandler(adapters.NewRepositoryAdapter(root.Repos.ScriptsRepo), log),
		log,
		middleware.FeatureFlagChecker("Script", scriptHistoryEnabled),
		scriptHistoryEnabled,
	), WithRegistrationPoint("register.ScriptHistory")); err != nil {
		return fmt.Errorf("wire registry: script-history module: %w", err)
	}
	return nil
}

// registerUtility wires the /utility module.
func registerUtility(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot) error {
	if err := tryRegisterModuleStrict(registry, log, module.NewUtilityModule(cfg, log, root.Utility.Utility), WithRegistrationPoint("register.Utility")); err != nil {
		return fmt.Errorf("wire registry: utility module: %w", err)
	}
	return nil
}

// registerRealtime wires the realtime module (clip-search lateral).
//
// Wave 15 (June 2026): DomainBundle.RealtimeMatcher is the typed
// assetsapi.RealtimeMatcher — drop the runtime cast.
//
// Note: realtimeEnabled is hardcoded false because the Realtime package
// was removed in commit d61068b3. The route-module closure still exists
// for future re-introduction.
func registerRealtime(registry *module.Registry, log *zap.Logger, root *ComposeRoot) error {
	if root.Domains == nil || root.Domains.RealtimeMatcher == nil {
		return nil
	}
	realtimeEnabled := false // Realtime package removed (commit d61068b3)
	matcher := root.Domains.RealtimeMatcher
	if err := tryRegisterModuleStrict(registry, log, module.NewRouteModule(
		"realtime",
		func() bool { return root.Domains.RealtimeMatcher != nil && realtimeEnabled },
		"",
		assetsapi.NewRealtimeMatchHandler(matcher, log),
		log,
	), WithRegistrationPoint("register.Realtime")); err != nil {
		return fmt.Errorf("wire registry: realtime module: %w", err)
	}
	return nil
}

// registerGenerationCapability wires the Capability-Standard unified
// generation endpoint at /api/generations via generation.Build(deps).
//
// Returning nil on Build failure keeps the registry mutable so
// WireRegistry's later phases are not poisoned. The strict path will
// surface a hard error on any subsequent register-generation attempt
// (pin per TestRegisterGenerationCapability_RepeatedCallsFailFast).
func registerGenerationCapability(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot) error {
	if root.Domains == nil {
		return nil
	}
	var booksHandler generation.HandlerFunc
	if root.Domains.BooksService != nil {
		booksHandler = root.Domains.BooksService.HandleJob
	}
	var lessonsHandler generation.HandlerFunc
	if root.Domains.LessonsService != nil {
		lessonsHandler = root.Domains.LessonsService.HandleJob
	}
	genDesc, err := generation.Build(generation.Dependencies{
		Jobs:           root.Jobs.Service,
		Assets:         root.Repos.Assets,
		Books:          booksHandler,
		Lessons:        lessonsHandler,
		BooksEnabled:   cfg.Books.Enabled,
		LessonsEnabled: cfg.Lessons.Enabled,
		ScriptEnabled:  anyScriptFeatureEnabled(cfg),
		Logger:         log,
	})
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "generation"), zap.Error(err))
		return nil
	}
	if err := tryRegisterModuleStrict(registry, log, genDesc, WithRegistrationPoint("register.Generation")); err != nil {
		return fmt.Errorf("wire registry: generation: %w", err)
	}
	// PublishSlots (api.DescriptorJobs). RegisterJobHandlers is the
	// slot-publication method; it does NOT re-register the module.
	// The strict-path Register call above already placed the Descriptor
	// in the module registry; this call only publishes worker handlers.
	if dj, ok := genDesc.(module.DescriptorJobs); ok {
		if err := dj.RegisterJobHandlers(root.Jobs.Service); err != nil {
			log.Warn("failed to register generation job handlers", zap.Error(err))
		}
	}
	return nil
}

// registerChannelsCapability wires the channels capability via
// channels.Build(deps). Build runs at most once per call; the resulting
// Descriptor is registered via tryRegisterModuleStrict exactly once.
func registerChannelsCapability(registry *module.Registry, log *zap.Logger, root *ComposeRoot) error {
	if root.DB == nil || root.DB.DB == nil {
		return nil
	}
	d, err := channels.Build(channels.Dependencies{
		Repository: channels.NewRepositoryAdapter(sqliteassets.NewChannelsRepository(root.DB.DB)),
		Logger:     log,
	})
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "channels"), zap.Error(err))
		return nil
	}
	if err := tryRegisterModuleStrict(registry, log, d, WithRegistrationPoint("register.Channels")); err != nil {
		return fmt.Errorf("wire registry: channels: %w", err)
	}
	return nil
}

// registerSearchQueriesCapability wires the typed search_queries use case.
// The handler is thin transport; this function owns the
// *searchqueries.UseCase construction (Wave 14 problem #3 close-out,
// June 2026) and registers the route module via tryRegisterModuleStrict.
func registerSearchQueriesCapability(registry *module.Registry, log *zap.Logger, root *ComposeRoot) error {
	if root.DB == nil || root.DB.DB == nil {
		return nil
	}
	if err := tryRegisterModuleStrict(registry, log, module.NewRouteModule(
		"search_queries",
		func() bool { return true },
		"/search-queries",
		assetsapi.NewSearchQueriesHandler(searchqueries.NewUseCase(sqliteassets.NewSearchQueriesRepository(root.DB.DB)), log),
		log,
	), WithRegistrationPoint("register.SearchQueries")); err != nil {
		return fmt.Errorf("wire registry: search_queries module: %w", err)
	}
	return nil
}
