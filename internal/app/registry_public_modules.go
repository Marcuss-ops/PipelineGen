// Package app — public route-module registrations (PR4 split).
//
// PR4 mechanical split (June 2026): relocated from registry.go without
// signature or behaviour changes. Each function in this file registers
// a thin route module that exposes an /api/* surface. Bundle-driven
// modules (Artlist, YouTubeClip, MediaIngest, Scraper,
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
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/admin"
	imagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/images"
	"github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/clipfolder"
	capsystem "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
	topicsourcecache "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/topicsourcecache"

	drive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// registerSystem wires the system module.
// PR3 (June 2026): Wave 14 close — the system module absorbed the
// former `internal/api/drive/` directory as a second receiver
// (DriveHandler) sharing the same /drive sub-group. The ctor takes
// driveUploader + reconcileSvc so /drive routes can answer (when
// either is nil the corresponding handler returns 503). The real Drive
// reconciler is not wired yet; keep this dependency nil so the API fails
// closed instead of returning a false empty success.
func registerSystem(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *wiring.ComposeRoot) error {
	// FASE 9 Step 2: use root.Drive.DriveUploader directly instead of
	// constructing a redundant *drive.Uploader from DriveClient.
	var driveUploaderAdapter *drive.Uploader
	if root.Drive != nil && root.Drive.DriveUploader != nil {
		driveUploaderAdapter = root.Drive.DriveUploader
	}
	capability := capsystem.NewModule(capsystem.Dependencies{
		Config:        doctorConfigFrom(cfg),
		Logger:        log,
		ToolChecker:   toolCheckerAdapter,
		ProcessRunner: processRunnerAdapter,
		DBHealth:      dbHealthCheckerAdapter,
		DriveOps:      newDriveAdminAdapter(driveUploaderAdapter, driveUploaderAdapter, root.Drive.Lifecycle, log),
	})
	if err := registry.RegisterCapabilityModule(capability, module.BuildContext{}); err != nil {
		return fmt.Errorf("wire registry: system capability: %w", err)
	}
	return nil
}

// registerImages wires the /images route module. Consumes
// wiring.MediaIngest.Service for upstream service injection.
//
// PR8 (June 2026): idemHandler installed on POST /api/media/ingest.
func registerImages(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *wiring.ComposeRoot, wiring *RegistryWiring) error {
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
func registerScriptHistory(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *wiring.ComposeRoot) error {
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
		scriptapi.NewScriptHistoryHandler(sqlitescripts.NewRepositoryAdapter(root.Repos.ScriptsRepo), log),
		log,
		middleware.FeatureFlagChecker("Script", scriptHistoryEnabled),
		scriptHistoryEnabled,
	), WithRegistrationPoint("register.ScriptHistory")); err != nil {
		return fmt.Errorf("wire registry: script-history module: %w", err)
	}
	return nil
}

// registerAdminModule wires the admin Drive canary capability via admin.Build.
//
// The admin module hosts operational readiness endpoints (Drive canary).
// Routes are protected by RequireAdminToken middleware using the
// canonical *config.Config as AuthSecurityPort.
//
// Step 3 of YouTube Clips Deploy Readiness action plan (July 2026).
func registerAdminModule(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *wiring.ComposeRoot) error {
	if root.Drive == nil || root.Drive.Publisher == nil {
		log.Warn("admin module skipped: Drive.Publisher not wired")
		return nil
	}

	// *config.Config satisfies middleware.AuthSecurityPort via its
	// EnableAuth() / AdminToken() / WorkerToken() methods.
	// Construct the canonical resolver once at the composition boundary;
	// the handler receives the typed resolver and never reads YAML itself.
	aliasResolver, err := clipfolder.NewFolderAliasResolverFromFile("config/folder_aliases.yaml")
	if err != nil {
		return fmt.Errorf("wire registry: admin: load folder alias resolver: %w", err)
	}
	adminDesc, err := admin.Build(admin.Dependencies{
		Publisher:           root.Drive.Publisher,
		FolderAliasResolver: aliasResolver,
		EnabledFunc:         func() bool { return true },
		ModuleOpts:          []module.RouteModuleOption{module.WithMiddleware(middleware.RequireAdminToken(cfg, log))},
		Logger:              log,
	})
	if err != nil {
		return fmt.Errorf("wire registry: admin: %w", err)
	}

	if err := tryRegisterModuleStrict(registry, log, adminDesc, WithRegistrationPoint("register.Admin")); err != nil {
		return fmt.Errorf("wire registry: admin: %w", err)
	}

	log.Info("admin module registered (drive canary: /api/drive/canary-upload)")
	return nil
}

// registerResearchCacheAdminModule wires the research-cache invalidation
// endpoint. It is registered independently of the Drive canary so cache
// invalidation does not depend on Drive being wired: the only requirement is
// the media DB (research_cache lives in root.DB.DB).
func registerResearchCacheAdminModule(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *wiring.ComposeRoot) error {
	if root.DB == nil || root.DB.DB == nil {
		log.Warn("research cache admin module skipped: DB not wired")
		return nil
	}

	handler := admin.NewResearchCacheInvalidateHandler(topicsourcecache.NewRepository(root.DB.DB), log)
	mod := module.NewRouteModule(
		"admin-research-cache",
		func() bool { return true },
		"/admin/research",
		handler,
		log,
		module.WithMiddleware(middleware.RequireAdminToken(cfg, log)),
	)
	if err := tryRegisterModuleStrict(registry, log, mod, WithRegistrationPoint("register.AdminResearchCache")); err != nil {
		return fmt.Errorf("wire registry: admin research cache: %w", err)
	}

	log.Info("admin research cache module registered (/api/admin/research/cache/invalidate)")
	return nil
}
