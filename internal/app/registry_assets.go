// Package app — Assets module registration + maintenance service wiring (PR4 split).
//
// PR4 mechanical split (June 2026): relocated from registry.go without
// signature or behaviour changes. The Assets bundle construction +
// maintenanceSvc lifecycle + DeletionService backfill cycle all live
// in this single file because they're a tightly-coupled cluster of
// pre/post-steps that need each other's state to be set in order:
//
//  1. Construct maintenanceSvc (depends on root.Maint.DeletionSvc +
//     root.Search.AssetIndexService/AssetTreeService + root.Jobs + DB.DB).
//  2. Construct voiceover wrapper from root.Domains.VoiceoverService.
//  3. Construct assetsDeps (consumes wiring.searchFanOut +
//     wiring.searchBackends + wiring.idempotencyHandler).
//  4. RegisterHandler on maintenanceSvc (early-failure annotation).
//  5. Call WireAssets(cfg, log, assetsDeps, ...).
//  6. Register Assets module on success.
//  7. Backfill maintenanceSvc.SetDeletionService(aw.DeletionSvc) so the
//     maintenance handlers can complete their deletion requests through
//     the Assets-side DeletionService implementation.
//
// The cycle is the original "PR4d-chunk2" pattern; PR4 just relocates
// it from registry.go into this dedicated file.
package app

import (
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	maintenance "github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	sqliteassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// registerAssets wires the Assets module + maintenance service.
//
// Consumes the cross-step state populated by registerInternalModules:
//   - wiring.searchFanOut + wiring.searchBackends (PR-2 single-instance
//     SearchFanOut stamped onto assetsBundle).
//   - wiring.idempotencyHandler (PR 8 shared idempotency middleware for
//     clips + register endpoints).
//
// Sets wiring.Assets on success.
func registerAssets(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, wiring *RegistryWiring) error {
	maintRepo := sqliteassets.NewMaintenanceRepository(root.DB.DB, log)
	maintenanceSvc := maintenance.NewService(cfg, log,
		root.Search.AssetIndexService,
		root.Search.AssetTreeService,
		root.Maint.DeletionSvc,
		root.Jobs.Service,
		maintRepo,
	)
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		log.Warn("failed to register maintenance handler", zap.Error(err))
	}

	// P0-2 commit 1 (June 2026): AssetsBundle renamed to
	// AssetsModuleDeps + regrouped by 4 real capability areas
	// (Core/Search/Delivery/Background). The 16 fields are kept
	// with identical names; the only change is grouping. See
	// assets_core.go for the sub-struct shape + regrouping
	// rationale.
	assetsDeps := &AssetsModuleDeps{
		Core: CoreDeps{
			ClipsRepo:          root.Repos.ClipsRepo,
			VoiceoverRepo:      root.Repos.VoiceoverRepo,
			ImageRepo:          root.Repos.ImageRepo,
			Assets:             root.Repos.Assets,
			AssetTreeService:   root.Search.AssetTreeService,
			AssetIndexService:  root.Search.AssetIndexService,
			MediaProcessor:     root.Process.MediaProcessor,
			CatalogSyncService: root.Sync.CatalogSync,
			ArtifactService:    root.Domains.ArtifactService,
		},
		Search: SearchDeps{
			ClipIndexerService: root.Process.ClipIndexerService,
			// MediasearchService + SearchWorkspaceID default to
			// zero value (nil + empty) in production; the
			// canonical SearchAggregator is wired with the
			// pre-built deps.Search.SearchFanOut (stamped by
			// WireRegistry). The typed slots live here for
			// future Wave 21 PR-9/10 follow-up that re-routes
			// tenant-isolation through AssetsBundle.
			SearchFanOut:          wiring.searchFanOut,
			SearchBackendRegistry: wiring.searchBackends,
			// Wave 6 (enforce aggregator singleton, July 2026): the
			// canonical *search.Aggregator singleton is plumbed from
			// RegistryWiring.searchAgg (constructed exactly once by
			// BuildCanonicalSearchFanOut inside
			// internal/app/search_backends.go and stamped by
			// registerSearchBackend). WireAssets MUST consume this
			// pointer rather than re-instantiating via
			// search.NewAggregator (per percheck_search_aggregator_singleton
			// forward-prevention gate).
			SearchAggregator: wiring.searchAgg,
		},
		Delivery: DeliveryDeps{
			Admin:     root.Drive.Admin,
			Publisher: root.Drive.Publisher,
		},
		Background: BackgroundDeps{
			IdempotencyStore:        root.Repos.IdempotencyStore,
			IdempotencyStoreHandler: wiring.idempotencyHandler,
		},
	}
	// Wave 16 (June 2026): WireAssets realtimeSvc is typed
	// `assetsapi.RealtimeMatcher` (no more `any` carrier).
	// Pass-through is direct: DomainBundle.RealtimeMatcher → WireAssets
	// (typed-to-typed, no auto-bridge required).
	//
	// PR-WIRE-ASSETS-CAPABILITY-SPLIT (2026-07-04, deadline 2026-08-15):
	// the YAGNI `catalogRepo "may reuse"` arg (was at position 8 in the
	// pre-split signature) is removed from the WireAssets call. The
	// appsearch.Service consumer that "may reuse" catalogRepo was
	// deleted in Wave 21 PR 10 (June 2026) and the param survived as
	// dead code until this split.
	aw, err := WireAssets(cfg, log, assetsDeps, root.Jobs, root.Drive.Lifecycle, root.Search.ProviderRegistry, root.Outbox.Dispatcher)
	if err != nil || aw == nil {
		return nil
	}
	wiring.Assets = aw
	if err := tryRegisterModuleStrict(registry, log, aw.Module, WithRegistrationPoint("register.Assets")); err != nil {
		return fmt.Errorf("wire registry: assets module: %w", err)
	}
	if maintenanceSvc != nil && aw.DeletionSvc != nil {
		maintenanceSvc.SetDeletionService(aw.DeletionSvc)
		log.Info("injected DeletionService into MaintenanceService")
	}
	return nil
}
