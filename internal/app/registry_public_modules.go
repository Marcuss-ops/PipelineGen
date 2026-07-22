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
	"path/filepath"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/admin"
	adminui "github.com/Marcuss-ops/PipelineGen/internal/api/admin/ui"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	channelsapi "github.com/Marcuss-ops/PipelineGen/internal/api/channels"
	imagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/images"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	scriptdocsapi "github.com/Marcuss-ops/PipelineGen/internal/api/script-docs"
	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/searchqueries"
	appchannels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	sqliteassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqlchannels "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/channels"

	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	scriptdocsinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/scriptdocs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/web"

	"go.uber.org/zap"
)

// registerSystem wires the system module.
// PR3 (June 2026): Wave 14 close — the system module absorbed the
// former `internal/api/drive/` directory as a second receiver
// (DriveHandler) sharing the same /drive sub-group. The ctor takes
// driveUploader + reconcileSvc so /drive routes can answer (when
// either is nil the corresponding handler returns 503).
func registerSystem(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot) error {
	// FASE 9 Step 2: use root.Drive.driveUploader directly instead of
	// constructing a redundant *drive.Uploader from DriveClient.
	var driveUploaderAdapter *drive.Uploader
	if root.Drive != nil && root.Drive.driveUploader != nil {
		driveUploaderAdapter = root.Drive.driveUploader
	}
	return tryRegisterModuleStrict(registry, log, systemapi.NewModule(
		doctorConfigFrom(cfg),
		log,
		toolCheckerAdapter, processRunnerAdapter, dbHealthCheckerAdapter,
		newDriveAdminAdapter(driveUploaderAdapter, driveUploaderAdapter, root.Drive.Lifecycle, log),
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
		Stats:       root.Jobs.Service,           // *appjobs.Service satisfies both kerneljob.Service + appjobs.JobStatsReader
		EnabledFunc: func() bool { return true }, // jobs is always on in production
		ModuleOpts:  nil,                         // no per-feature middleware (matches pre-Step-13 wiring)
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

// registerChannelsCapability wires the channels capability via
// channels.Build(deps). Build runs at most once per call; the resulting
// Descriptor is registered via tryRegisterModuleStrict exactly once.
func registerChannelsCapability(registry *module.Registry, log *zap.Logger, root *ComposeRoot) error {
	if root.DB == nil || root.DB.DB == nil {
		return nil
	}
	d, err := channelsapi.Build(channelsapi.Dependencies{
		Repository: appchannels.NewRepositoryAdapter(sqlchannels.NewChannelsRepository(root.DB.DB)),
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

// registerScriptDocs wires the /api/script-docs/* capability via
// scriptdocs.Build. Closes PR-SCRIPT-DOCS-DRIFT-2026-07-08: the
// canonical /api/script-docs/generate route is now mounted (returns
// 503 with ErrReActNotWired diagnostic when the typed ReActPort is
// not wired at the composition root — the canonical pre-fail-closed
// posture for optional modules).
//
// The route module is gated on cfg.Features.ScriptDocsEnabled
// (canonical ScriptDocsEnabled feature flag from
// internal/platform/config/types_misc.go). The ReAct typed port
// itself is nil-tolerant — composition root passes nil today (the
// Python ReAct bridge is a forward-pointer CUTOVER); a future
// CUTOVER injects a concrete adapter.
//
// Step 3 placement: script-docs routes are mounted alongside the
// script-flow routes (both are script-domain surfaces). Mirrors
// the registerScripts placement at Step 3.
func registerScriptDocs(registry *module.Registry, log *zap.Logger, cfg *config.Config) error {
	// Wire the concrete ReActPort adapter when Ollama URL is configured.
	// If adapter construction fails (misconfigured), fall back to nil port
	// (handler returns 503 ErrReActNotWired per godlike/07 fail-closed).
	var reactPort scriptdocsapi.ReActPort
	if cfg.External.OllamaURL != "" {
		// Resolve the project root to an absolute path so the Python bridge
		// script is found regardless of the server's working directory
		// (systemd, Docker, nohup may set CWD to /).
		scriptDir, scriptDirErr := filepath.Abs(".")
		if scriptDirErr != nil {
			log.Warn("script-docs: failed to resolve project root, falling back to nil port",
				zap.Error(scriptDirErr))
		} else {
			adapter, adapterErr := scriptdocsinfra.NewAdapter(scriptdocsinfra.AdapterConfig{
				OllamaURL:   cfg.External.OllamaURL,
				OllamaModel: cfg.External.OllamaModel,
				ScriptDir:   scriptDir,
			})
			if adapterErr != nil {
				log.Warn("script-docs: failed to create ReAct adapter, falling back to nil port",
					zap.Error(adapterErr))
			} else {
				reactPort = adapter
				log.Info("script-docs: ReAct adapter wired (Python bridge via Ollama)",
					zap.String("ollama_url", cfg.External.OllamaURL),
					zap.String("ollama_model", cfg.External.OllamaModel))
			}
		}
	}

	scriptDocsDesc, err := scriptdocsapi.Build(scriptdocsapi.Dependencies{
		Port:        reactPort,
		EnabledFunc: func() bool { return cfg.Features.ScriptDocsEnabled },
		ModuleOpts:  nil, // no per-feature middleware (matches the script pattern)
		Logger:      log,
	})
	if err != nil {
		return fmt.Errorf("wire registry: script-docs: %w", err)
	}
	sdd, ok := scriptDocsDesc.(*scriptdocsapi.ScriptDocsDescriptor)
	if !ok || sdd == nil {
		return fmt.Errorf("wire registry: script-docs: scriptdocs.Build returned unexpected descriptor type %T (want *scriptdocsapi.ScriptDocsDescriptor)", scriptDocsDesc)
	}
	log.Info("created ScriptDocs module (ReAct typed port wired at composition time; nil-port pre-CUTOVER returns 503 ErrReActNotWired)")
	return tryRegisterModuleStrict(registry, log, sdd, WithRegistrationPoint("register.ScriptDocs"))
}

// registerAdminModule wires the /api/admin/* capability via admin.Build.
//
// The admin module hosts operational readiness endpoints (Drive canary).
// Routes are protected by RequireAdminToken middleware using the
// canonical *config.Config as AuthSecurityPort.
//
// Step 3 of YouTube Clips Deploy Readiness action plan (July 2026).
func registerAdminModule(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot) error {
	if root.Drive == nil || root.Drive.Publisher == nil {
		log.Warn("admin module skipped: Drive.Publisher not wired")
		return nil
	}

	// *config.Config satisfies middleware.AuthSecurityPort via its
	// EnableAuth() / AdminToken() / WorkerToken() methods.
	adminDesc, err := admin.Build(admin.Dependencies{
		Publisher:   root.Drive.Publisher,
		EnabledFunc: func() bool { return true },
		ModuleOpts:  []module.RouteModuleOption{module.WithMiddleware(middleware.RequireAdminToken(cfg, log))},
		Logger:      log,
	})
	if err != nil {
		return fmt.Errorf("wire registry: admin: %w", err)
	}

	if err := tryRegisterModuleStrict(registry, log, adminDesc, WithRegistrationPoint("register.Admin")); err != nil {
		return fmt.Errorf("wire registry: admin: %w", err)
	}

	log.Info("admin module registered (drive canary: /api/admin/drive/canary-upload)")
	return nil
}

// registerAdminUIModule wires the /api/admin/ui/* capability via adminui.Build.
// The static React app is embedded via web.DistFS and served from /admin by routes.go.
func registerAdminUIModule(registry *module.Registry, log *zap.Logger, cfg *config.Config) error {
	adminUIDesc, err := adminui.Build(adminui.Dependencies{
		StaticFS:    web.DistFS(),
		EnabledFunc: func() bool { return true },
		ModuleOpts: []module.RouteModuleOption{
			module.WithMiddleware(middleware.RequireAdminToken(cfg, log)),
		},
		Logger: log,
	})
	if err != nil {
		return fmt.Errorf("wire registry: admin-ui: %w", err)
	}
	if err := tryRegisterModuleStrict(registry, log, adminUIDesc, WithRegistrationPoint("register.AdminUI")); err != nil {
		return fmt.Errorf("wire registry: admin-ui: %w", err)
	}
	log.Info("admin-ui module registered (/api/admin/ui/*)")
	return nil
}
