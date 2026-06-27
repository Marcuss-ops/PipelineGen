// Package app — registry composition (PR4d-final: takes *ComposeRoot).
//
// PR4d-final (June 2026): WireRegistry takes ONLY *ComposeRoot + ctx.
// The legacy *CoreDeps projection was deleted; all reads inside WireRegistry
// (the ScriptFlow inline block, the late-bindings, the channels/content/
// search-queries/utility module registrations) now source from
// root.<bundle>.<field> directly.
//
// Body is structurally identical to pre-PR4d: build RegistryWiring,
// late-inject ImageService → MediaIngest Service, mutate
// ProviderRegistry.Freeze() at the very end of WireRegistry (Reviewer Q8 fix).
package app

import (
	"context"
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	imagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/images"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	outboxapi "github.com/Marcuss-ops/PipelineGen/internal/api/outbox"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	artlistadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	stockadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock"
	youtubeadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/youtube"
	searchqueriesuc "github.com/Marcuss-ops/PipelineGen/internal/application/assets/searchqueries"
	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptassets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"

	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/api/mediasearch"
	generation "github.com/Marcuss-ops/PipelineGen/internal/application/generation"
	mediasearch "github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// RegistryWiring holds the registry and all wired modules.
// PR2 (June 2026): removed System, Jobs, Images, Drive, Scraper — those were
// thin Wire wrappers inlined directly in WireRegistry below.
type RegistryWiring struct {
	Registry      *module.Registry
	ArtlistSvc    *ArtlistWiring
	YouTubeClip   *YouTubeClipWiring
	MediaIngest   *MediaIngestWiring
	Assets        *AssetsWiring
	FullImages    *FullImagesWiring
	StockPipeline *StockPipelineWiring

	// QDRANT-002 + QDRANT-004 separation-of-routes (June 2026):
	// These handlers are constructed by WireRegistry but NOT registered
	// in the public /api registry. They are plumbed through AppDeps and
	// mounted on the /internal/v1 WorkerAuth-protected internalGroup
	// by cmd/server/main.go. The split is enforced by the
	// anti-regression test internal/api/routes_test.go.
	OutboxHandler      interface{ RegisterRoutes(*gin.RouterGroup) }
	MediasearchHandler interface{ RegisterRoutes(*gin.RouterGroup) }
}

// tryRegisterModuleStrict is the strict-fail variant. Every WireRegistry
// call site uses this helper; the previous coalescing tryRegisterModule
// wrapper was deleted in PR 1 (June 2026) so duplicate module
// publication surfaces as a hard error instead of silently dropping
// the duplicate on a Debug log. Cross-slot publication (DescriptorJobs /
// DescriptorProviders publishing the same capability name through a
// shared Descriptor) registers to DISTINCT registries (module.Registry
// vs Jobs.Service vs providers.Registry), so the strict path is safe.
//
// The "compose:" prefix on the wrapped error is pinned by
// TestTryRegisterModule_ErrorContainsSpecMarker in
// internal/app/registry_failfast_test.go; do not change without updating
// the test marker.
func tryRegisterModuleStrict(registry *module.Registry, log *zap.Logger, mod module.Module) error {
	if err := registry.Register(mod); err != nil {
		// Guarded nil-log path for the failfast test contract
		// (registry_failfast_test.go::TestTryRegisterModule_DuplicateFails
		// passes `nil` for the logger to keep the assertion pure). Production
		// call sites always wire a real *zap.Logger from WireRegistry's
		// zap parameter — the Warn at the duplicate-name site keeps
		// observability intact there.
		if log != nil {
			log.Warn("failed to register module", zap.String("module", mod.Name()), zap.Error(err))
		}
		return fmt.Errorf("compose: module=%q already registered: %w", mod.Name(), err)
	}
	return nil
}

// WireRegistry creates and populates the module registry with all modules.
//
// PR4d-final (June 2026): signature takes (ctx, cfg, log, root). The
// transitional cd parameter was removed. All reads source from root.<bundle>.
func WireRegistry(ctx context.Context, cfg *config.Config, log *zap.Logger, root *ComposeRoot) (*RegistryWiring, error) {
	if root == nil {
		return nil, fmt.Errorf("wire registry: compose root is nil")
	}

	registry := module.NewRegistry()
	wiring := &RegistryWiring{Registry: registry}

	// System module — no deps (PR2: inlined from WireSystem).
	// PR3 (June 2026): Wave 14 close — the system module absorbed the
	// former `internal/api/drive/` directory as a second receiver
	// (DriveHandler) sharing the same /drive sub-group. The ctor takes
	// driveUploader + reconcileSvc so /drive routes can answer (when
	// either is nil the corresponding handler returns 503).
	var driveUploaderAdapter *driveup.Uploader
	if root.Drive != nil && root.Drive.DriveClient != nil {
		driveUploaderAdapter = &driveup.Uploader{Service: root.Drive.DriveClient, Log: log}
	}
	if err := tryRegisterModuleStrict(registry, log, systemapi.NewModule(
		doctorConfigFrom(cfg),
		log,
		toolCheckerAdapter, processRunnerAdapter, dbHealthCheckerAdapter,
		newDriveAdminAdapter(driveUploaderAdapter, log),
		&noopReconciler{},
	)); err != nil {
		return nil, fmt.Errorf("wire registry: system module: %w", err)
	}

	// Artlist (PR4d-chunk2): takes *ArtlistBundle + vectorStore.
	artlistBundle := &ArtlistBundle{
		DB:                 root.DB,
		Assets:             root.Repos.Assets,
		ClipsRepo:          root.Repos.ClipsRepo,
		DriveClient:        root.Drive.DriveClient,
		DriveUploader:      root.Drive.DriveUploader,
		AssetIndexService:  root.Search.AssetIndexService,
		ClipIndexerService: root.Process.ClipIndexerService,
		MediaProcessor:     root.Process.MediaProcessor,
		Jobs:               root.Jobs,
		CatalogSyncService: root.Sync.CatalogSync,
	}
	if aw, err := WireArtlist(ctx, cfg, log, artlistBundle, root.Outbox.Dispatcher); err != nil {
		log.Warn("failed to wire module", zap.String("module", "Artlist"), zap.Error(err))
	} else {
		if err := tryRegisterModuleStrict(registry, log, aw.Module); err != nil {
			return nil, fmt.Errorf("wire registry: artlist: %w", err)
		}
		wiring.ArtlistSvc = aw
	}

	// ScriptFlow — sources from root.<bundle>.<field>. Extracted into
	// wireScriptFlow (PR7 cleanup, June 2026) to shrink WireRegistry and
	// reuse the canonical engine + memorySvc from AIBundle.
	if err := wireScriptFlow(ctx, cfg, log, root, registry); err != nil {
		return nil, fmt.Errorf("wire registry: script-flow: %w", err)
	}

	// YouTubeClip (PR4d-chunk2): 4 direct narrow args + ProviderRegistry.
	// ProviderRegistry is not yet populated when WireYouTubeClip runs —
	// the handler's constructor resolves providers lazily so it's fine
	// to pass the empty registry here; it will be populated by the time
	// HTTP requests arrive.
	// PR8 (June 2026): IdempPlus constructs the canonical reusable Gin
	// idempotency middleware instance from RepoBundle.IdempotencyStore;
	// shared across YouTubeClip, MediaIngest, and (via AssetsBundle)
	// clips + register handlers.
	idemPlus := middleware.NewIdempotency(root.Repos.IdempotencyStore, log)
	idemHandler := idemPlus.Handler()
	if yw, err := WireYouTubeClip(cfg, log, root.Domains.YoutubeClipService, root.Jobs.Facade, root.Jobs.Service, root.Repos.ClipsRepo, root.Search.ProviderRegistry, toolCheckerAdapter, idemHandler); err != nil {
		log.Warn("failed to wire module", zap.String("module", "YouTubeClip"), zap.Error(err))
	} else {
		if err := tryRegisterModuleStrict(registry, log, yw.Module); err != nil {
			return nil, fmt.Errorf("wire registry: youtube: %w", err)
		}
		wiring.YouTubeClip = yw
	}

	// ── Per-capability inventory (PR 1, June 2026): UNROLLED from the
	// previous `for _, m := range []struct{...}` loop so:
	//   1. Each capability has exactly one typed block at the composition
	//      site (no anonymous function-tuple shape).
	//   2. The 4 side-builds (realtime / generation / channels /
	//      search_queries) that were trapped INSIDE the previous loop body
	//      now execute EXACTLY ONCE — they had no dependence on the loop
	//      iteration variable. The hoisted block is the next section
	//      below the inventory.
	// Each block follows the same shape: build → tryRegisterModuleStrict
	// → propagate wiring handle. The strict path is mandatory after the
	// PR 1 deletion of the coalescing tryRegisterModule safe-skip helper
	// (silent duplicates now surface as hard composition errors).
	var imagesHandler *imagesapi.ImagesHandler

	// 1) Jobs — thin wrapper, no bundle deps.
	{
		jobsHandler := jobsapi.NewJobsHandler(root.Jobs.Service, log)
		jobsMod := module.NewRouteModule(
			"jobs",
			func() bool { return true },
			"/jobs",
			jobsHandler,
			log,
		)
		log.Info("created Jobs module")
		if err := tryRegisterModuleStrict(registry, log, jobsMod); err != nil {
			return nil, fmt.Errorf("wire registry: jobs: %w", err)
		}
	}

	// 2) Images — needs MediaIngest wiring for upstream service injection.
	{
		var ingestSvc *ingest.Service
		if wiring.MediaIngest != nil {
			ingestSvc = wiring.MediaIngest.Service
		}
		imagesHandler = imagesapi.NewImagesHandler(root.Domains.ImageService, ingestSvc)
		imagesMod := module.NewRouteModule(
			"images",
			func() bool { return cfg.Features.ImagesEnabled },
			"/images",
			imagesHandler,
			log,
		)
		log.Info("created Images module")
		if err := tryRegisterModuleStrict(registry, log, imagesMod); err != nil {
			return nil, fmt.Errorf("wire registry: images: %w", err)
		}
	}

	// 3) MediaIngest — bundle-driven; reuses root.<bundle> paths. PR8:
	// idemHandler installed on POST /api/media/ingest.
	{
		ingestBundle := &MediaIngestBundle{
			DB:                root.DB,
			Assets:            root.Repos.Assets,
			DriveClient:       root.Drive.DriveClient,
			ImageRepo:         root.Repos.ImageRepo,
			VoiceoverRepo:     root.Repos.VoiceoverRepo,
			ClipsRepo:         root.Repos.ClipsRepo,
			AssetIndexService: root.Search.AssetIndexService,
			PrebuiltService:   root.Domains.IngestService,
		}
		mediaIngestW, mediaIngestErr := WireMediaIngest(cfg, log, ingestBundle, idemHandler)
		wiring.MediaIngest = mediaIngestW
		if mediaIngestErr != nil {
			log.Warn("failed to wire module", zap.String("module", "MediaIngest"), zap.Error(mediaIngestErr))
		} else if mediaIngestW != nil && mediaIngestW.Module != nil {
			if err := tryRegisterModuleStrict(registry, log, mediaIngestW.Module); err != nil {
				return nil, fmt.Errorf("wire registry: media-ingest: %w", err)
			}
		}
	}

	// 4) Scraper — thin wrapper, infra-only deps.
	{
		scraperHandler := assetsapi.NewScraperHandler(cfg.External.NodeScraperDir, processRunnerAdapter)
		scraperMod := module.NewRouteModule(
			"scraper",
			func() bool { return scraperHandler != nil },
			"/scraper",
			scraperHandler,
			log,
		)
		log.Info("created Scraper module")
		if err := tryRegisterModuleStrict(registry, log, scraperMod); err != nil {
			return nil, fmt.Errorf("wire registry: scraper: %w", err)
		}
	}

	// 5) FullImages — bundle-driven; uses ImageService + MediaStore.
	{
		fullImagesW, fullImagesErr := WireFullImages(cfg, log, root.Domains.ImageService, root.Drive.MediaStore)
		wiring.FullImages = fullImagesW
		if fullImagesErr != nil {
			log.Warn("failed to wire module", zap.String("module", "FullImages"), zap.Error(fullImagesErr))
		} else if fullImagesW != nil && fullImagesW.Module != nil {
			if err := tryRegisterModuleStrict(registry, log, fullImagesW.Module); err != nil {
				return nil, fmt.Errorf("wire registry: full-images: %w", err)
			}
		}
	}

	// 6) StockPipeline — bundle-driven.
	{
		stockBundle := &StockBundle{
			DriveClient:        root.Drive.DriveClient,
			Jobs:               root.Jobs.Service,
			JobFacade:          root.Jobs.Facade,
			AssetIndexService:  root.Search.AssetIndexService,
			ClipsRepo:          root.Repos.ClipsRepo,
			YoutubeClipService: root.Domains.YoutubeClipService,
			ClipIndexerService: root.Process.ClipIndexerService,
			Dispatcher:         root.Outbox.Dispatcher,
		}
		stockW, stockErr := WireStockPipeline(cfg, log, stockBundle)
		wiring.StockPipeline = stockW
		if stockErr != nil {
			log.Warn("failed to wire module", zap.String("module", "StockPipeline"), zap.Error(stockErr))
		} else if stockW != nil && stockW.Module != nil {
			if err := tryRegisterModuleStrict(registry, log, stockW.Module); err != nil {
				return nil, fmt.Errorf("wire registry: stock-pipeline: %w", err)
			}
		}
	}

	// ─────────────────────────────────────────────────────────────────
	// HOISTED SIDE-BUILDS (PR 1, June 2026) — execute ONCE, not 6×.
	// The 4 capability clusters below were trapped inside the previous
	// `for` body, which meant they executed per iteration. They have
	// NO dependence on the loop iteration variable and now run exactly
	// once, in a deterministic order. Each call site uses the strict
	// helper (tryRegisterModuleStrict).
	// ─────────────────────────────────────────────────────────────────

	// realtime (clip-search lateral capability; Wave 14 close).
	// PR3 (June 2026): Wave 14 close — moved from internal/api/realtime/
	// to internal/api/assets/handler_realtime.go as RealtimeMatchHandler.
	// Wave 15 (June 2026): DomainBundle.RealtimeMatcher is the typed
	// assetsapi.RealtimeMatcher — drop the runtime cast.
	if root.Domains != nil && root.Domains.RealtimeMatcher != nil {
		realtimeEnabled := false // Realtime package removed (commit d61068b3)
		matcher := root.Domains.RealtimeMatcher
		if err := tryRegisterModuleStrict(registry, log, module.NewRouteModule(
			"realtime",
			func() bool { return root.Domains.RealtimeMatcher != nil && realtimeEnabled },
			"",
			assetsapi.NewRealtimeMatchHandler(matcher, log),
			log,
		)); err != nil {
			return nil, fmt.Errorf("wire registry: realtime module: %w", err)
		}
	}

	// ── Unified generation API (replaces /api/books + /api/lessons) ──
	// Capability Standard migration (June 2026). Worker-side handler-function
	// values are passed via nil-guarded method-value extraction. The strict
	// path is mandatory now (PR 1) — duplicate names surface as errors.
	var booksHandler generation.HandlerFunc
	if root.Domains != nil && root.Domains.BooksService != nil {
		booksHandler = root.Domains.BooksService.HandleJob
	}
	var lessonsHandler generation.HandlerFunc
	if root.Domains != nil && root.Domains.LessonsService != nil {
		lessonsHandler = root.Domains.LessonsService.HandleJob
	}
	if genDesc, genErr := generation.Build(generation.Dependencies{
		Jobs:           root.Jobs.Service,
		Assets:         root.Repos.Assets,
		Books:          booksHandler,
		Lessons:        lessonsHandler,
		BooksEnabled:   cfg.Books.Enabled,
		LessonsEnabled: cfg.Lessons.Enabled,
		ScriptEnabled:  anyScriptFeatureEnabled(cfg),
		Logger:         log,
	}); genErr != nil {
		log.Warn("failed to wire module", zap.String("module", "generation"), zap.Error(genErr))
	} else {
		if err := tryRegisterModuleStrict(registry, log, genDesc); err != nil {
			return nil, fmt.Errorf("wire registry: generation: %w", err)
		}
		// *GenerationDescriptor satisfies api.Descriptor via the three
		// explicit delegation methods (Name/Enabled/RegisterRoutes), and
		// api.DescriptorJobs via RegisterJobHandlers. The cast goes
		// directly against the concrete pointer.
		if dj, ok := genDesc.(module.DescriptorJobs); ok {
			if err := dj.RegisterJobHandlers(root.Jobs.Service); err != nil {
				log.Warn("failed to register generation job handlers", zap.Error(err))
			}
		}
	}

	// channels (Capability Standard migration, June 2026).
	if root.DB != nil && root.DB.DB != nil {
		if d, err := channels.Build(channels.Dependencies{
			Repository: channels.NewRepositoryAdapter(assets.NewChannelsRepository(root.DB.DB)),
			Logger:     log,
		}); err != nil {
			log.Warn("failed to wire module", zap.String("module", "channels"), zap.Error(err))
		} else {
			if err := tryRegisterModuleStrict(registry, log, d); err != nil {
				return nil, fmt.Errorf("wire registry: channels: %w", err)
			}
		}
	}

	// search_queries (Wave 14 PR5, typed-use-case injection).
	// PR3 (June 2026): Wave 14 close — moved from internal/api/searchqueries/
	// to internal/api/assets/handler_searchqueries.go as SearchQueriesHandler.
	// Composition builds the *searchqueriesuc.UseCase from the concrete
	// repo and injects it into the handler — keeping handler = thin transport.
	if err := tryRegisterModuleStrict(registry, log, module.NewRouteModule(
		"search_queries",
		func() bool { return true },
		"/search-queries",
		assetsapi.NewSearchQueriesHandler(searchqueriesuc.NewUseCase(assets.NewSearchQueriesRepository(root.DB.DB)), log),
		log,
	)); err != nil {
		return nil, fmt.Errorf("wire registry: search_queries module: %w", err)
	}

	if wiring.MediaIngest != nil {
		log.Info("MediaIngest module wired (service pre-built via BuildDomainBundle, no late-binding needed)")
	}
	if root.Repos != nil && root.Repos.ScriptsRepo != nil {
		// NewScriptHistoryModule expects two gin.HandlerFunc gate args
		// (handler feature gate + enabled bool). The helper in
		// internal/api/middleware reads the resolved boolean and wraps
		// it in a 403-on-disabled middleware. Script history is shared by
		// all script entrypoints, so we keep it alive whenever any script
		// feature is enabled.
		scriptHistoryEnabled := anyScriptFeatureEnabled(cfg)
		if err := tryRegisterModuleStrict(registry, log, scriptapi.NewScriptHistoryModule(
			scriptapi.NewScriptHistoryHandler(scriptcore.NewRepositoryAdapter(root.Repos.ScriptsRepo), log),
			log,
			middleware.FeatureFlagChecker("Script", scriptHistoryEnabled),
			scriptHistoryEnabled,
		)); err != nil {
			return nil, fmt.Errorf("wire registry: script-history module: %w", err)
		}
	}
	if err := tryRegisterModuleStrict(registry, log, module.NewUtilityModule(cfg, log, root.Utility.Utility)); err != nil {
		return nil, fmt.Errorf("wire registry: utility module: %w", err)
	}

	// PR4d-chunk2: maintenanceSvc constructed locally (no longer assigned to CoreDeps);
	// voiceoverSvc selected from root.Domains; assets bundle built from root.
	maintenanceSvc := maintenance.NewService(cfg, log, root.Search.AssetIndexService, root.Search.AssetTreeService, root.Maint.DeletionSvc, root.Jobs.Service, root.DB.DB)
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		log.Warn("failed to register maintenance handler", zap.Error(err))
	}

	var voiceoverService *voiceover.Service
	if root.Domains.VoiceoverService != nil {
		voiceoverService = root.Domains.VoiceoverService
	}

	assetsBundle := &AssetsBundle{
		ClipsRepo:               root.Repos.ClipsRepo,
		VoiceoverRepo:           root.Repos.VoiceoverRepo,
		ImageRepo:               root.Repos.ImageRepo,
		Assets:                  root.Repos.Assets,
		DriveClient:             root.Drive.DriveClient,
		AssetTreeService:        root.Search.AssetTreeService,
		AssetIndexService:       root.Search.AssetIndexService,
		MediaProcessor:          root.Process.MediaProcessor,
		CatalogSyncService:      root.Sync.CatalogSync,
		ClipIndexerService:      root.Process.ClipIndexerService,
		IdempotencyStore:        root.Repos.IdempotencyStore,
		IdempotencyStoreHandler: idemHandler,
	}
	// Wave 16 (June 2026): WireAssets realtimeSvc is typed
	// `assetsapi.RealtimeMatcher` (no more `interface{}` carrier).
	// Pass-through is direct: DomainBundle.RealtimeMatcher → WireAssets
	// (typed-to-typed, no auto-bridge required).
	if aw, err := WireAssets(cfg, log, assetsBundle, root.Jobs, voiceoverService, root.Domains.VoiceoverSync, root.Domains.RealtimeMatcher, root.Repos.CatalogRepo, maintenanceSvc, root.Search.ProviderRegistry, root.Outbox.Dispatcher); err == nil && aw != nil {
		wiring.Assets = aw
		if err := tryRegisterModuleStrict(registry, log, aw.Module); err != nil {
			return nil, fmt.Errorf("wire registry: assets module: %w", err)
		}
		if maintenanceSvc != nil && aw.DeletionSvc != nil {
			maintenanceSvc.SetDeletionService(aw.DeletionSvc)
			log.Info("injected DeletionService into MaintenanceService")
		}
	}

	// ── QDRANT-002: build canonical internal outbox handler ─────────
	// Exposes GET /internal/v1/outbox/status and /events for operator
	// dashboard visibility into the outbox events pipeline (pending,
	// processing, dead_letter, completed, superseded counts + event list).
	//
	// QDRANT-002 (June 2026) separation-of-routes fix: the handler is
	// constructed here but NOT registered in the public /api registry —
	// that caused /api/internal/v1/outbox/* to leak past the WorkerAuth
	// boundary. The handler is now passed to AppDeps.OutboxHandler and
	// mounted on the /internal/v1 WorkerAuth-protected internalGroup by
	// cmd/server/main.go. See internal/api/routes.go::Setup for the
	// wiring site; the test internal/api/routes_test.go::TestRoutes_
	// NoApiInternalV1Prefix enforces this split at CI time.
	if root.Outbox != nil && root.Outbox.EventsRepo != nil {
		// Wave 14 PR5 (June 2026): wrap the concrete *outboxevents.Repository
		// in a typed outbox.MonitorPort adapter so the api layer stays free of
		// internal/infrastructure/* imports. Adapter is constructed here
		// because the api package must not import outboxevents directly per
		// AGENTS.md Pattern 8 ("API package: thin transport only").
		outboxPort := newOutboxMonitorAdapter(root.Outbox.EventsRepo)
		outboxH := outboxapi.NewHandler(outboxPort, log)
		wiring.OutboxHandler = outboxH
		log.Info("QDRANT-002: outbox events handler BUILT (mounted on /internal/v1/outbox via AppDeps, NOT via /api)")
	}

	// ── QDRANT-004: build mediasearch handler ─────────────────────────
	// Wires the unified media search API at POST /internal/v1/media/search
	// when Qdrant is enabled and the vector store adapter is available.
	//
	// QDRANT-004 (June 2026) separation-of-routes fix: same reasoning as
	// the outbox handler above — the handler is constructed here but NOT
	// registered through the public /api registry (which would mount it
	// at /api/internal/v1/media/* outside the WorkerAuth boundary).
	// AppDeps.MediasearchHandler is mounted on internalGroup by
	// cmd/server/main.go.
	if root.Process.VectorSvc != nil && root.AI != nil && root.AI.OllamaClient != nil {
		// Wave 15 (June 2026): ProcessBundle.VectorSvc is the typed
		// assetsearch.VectorStorePort — no runtime cast needed.
		// portutil.IsNilPort is the typed-nil safety net (catches the
		// `(*searchAdapter)(nil)` case if a future refactor accidentally
		// injects a typed-nil concrete; the field type guard above is
		// the front line).
		vectorStore := root.Process.VectorSvc
		if vectorStore != nil && !portutil.IsNilPort(vectorStore) {
			// Build the VectorSearchPort adapter: OllamaClient for embedding +
			// Qdrant search adapter for vector store operations.
			// Wave 15 (June 2026): ProcessBundle.VectorSvc is the typed
			// assetsearch.VectorStorePort — `vectorStore` above is direct read.
			// Compile-time assertion at internal/infrastructure/qdrant/search_adapter.go
			// guarantees the qdrant adapter satisfies the port.
			vectorPort := &mediasearchVectorAdapter{
				embedder: root.AI.OllamaClient,
				store:    vectorStore,
			}
			readRepo := &mediasearchReadAdapter{clips: root.Repos.ClipsRepo}
			deliverySvc, err := buildMediasearchDeliverySvc(
				cfg.Security.DeliveryHMACSecret,
				cfg.External.VeloxBaseURL,
				cfg.Security.DeliveryReplayWindowSec,
				log,
			)
			if err != nil {
				log.Warn("QDRANT-004: mediasearch delivery signer unavailable, skipping mediasearch handler", zap.Error(err))
				// QDRANT-004 closed (June 2026): when the delivery signer
				// can't be built, mediasearch is disabled rather than
				// serving results without authorized download URLs.
				// The handler is not wired; /internal/v1/media/search
				// will 404 (no route registered).
				return wiring, fmt.Errorf("mediasearch delivery signer: %w", err)
			}

			searchSvc := mediasearch.NewService(
				vectorPort,
				readRepo,
				deliverySvc,
				mediasearch.Config{},
				mediasearchLogger{sugar: log.Sugar()},
			)
			searchH := mediasearchapi.NewHandler(searchSvc, log)
			wiring.MediasearchHandler = searchH
			log.Info("QDRANT-004: mediasearch handler BUILT (mounted on /internal/v1/media/search via AppDeps, NOT via /api)")
		}
	}

	// ── ProviderRegistry — register adapters + FREEZE at the end ─────
	// Lives on SearchBundle (PR4 review): it's an asset-search dispatch
	// registry, not a Drive-sync concern.
	if root.Search != nil && root.Search.ProviderRegistry != nil {
		pr := root.Search.ProviderRegistry
		if wiring.ArtlistSvc != nil && wiring.ArtlistSvc.Service != nil {
			if err := pr.RegisterSearch(artlistadapter.NewAdapter(wiring.ArtlistSvc.Service)); err != nil {
				log.Warn("failed to register artlist provider", zap.Error(err))
			} else {
				log.Info("registered artlist provider in providers.Registry")
			}
		} else {
			log.Info("artlist service unavailable — skipping provider registration")
		}
		if wiring.YouTubeClip != nil && wiring.YouTubeClip.Service != nil {
			if err := pr.RegisterSearch(youtubeadapter.NewAdapter(wiring.YouTubeClip.Service)); err != nil {
				log.Warn("failed to register youtube provider", zap.Error(err))
			} else {
				log.Info("registered youtube provider in providers.Registry")
			}
		} else {
			log.Info("youtube clip service unavailable — skipping provider registration")
		}
		if wiring.StockPipeline != nil && wiring.StockPipeline.Service != nil {
			if err := pr.RegisterFetch(stockadapter.NewAdapter(wiring.StockPipeline.Service)); err != nil {
				log.Warn("failed to register stock fetch provider", zap.Error(err))
			} else {
				log.Info("registered stock fetch provider in providers.Registry")
			}
		} else {
			log.Info("stock pipeline service unavailable — skipping fetch provider registration")
		}
		// ── ScriptAssets capability (Capability Standard DescriptorProviders
		// slot migration, June 2026): the script_assets capability is wired
		// via scriptassets.Build(deps). Build returns a single Descriptor that
		// carries:
		//   - the api.Module for /script-assets routes, AND
		//   - the api.DescriptorProviders slot which the composition root uses
		//     to publish the script_assets catalog entry (provider identity +
		//     capabilities) into the canonical providers.Registry.
		//
		// This is the "richer" capability migration demonstrating the slot
		// pattern's RANGE beyond DescriptorJobs: DescriptorProviders is a
		// one-shot composition-time publication of catalog identity (not per-
		// job runtime registration like DescriptorJobs). Both slots coexist
		// on the same Descriptor mechanism; the composition root type-asserts
		// for each independently.
		//
		// RegisterProviders must run BEFORE pr.Freeze() below; the registry
		// must be mutable when the descriptor publishes into it. Frozen
		// registries return ErrFrozen from Register, so ordering matters.
		scDesc, scErr := scriptassets.Build(scriptassets.Dependencies{
			Logger: log,
		})
		if scErr != nil {
			log.Warn("failed to wire module", zap.String("module", "script-assets"), zap.Error(scErr))
		} else {
			if err := tryRegisterModuleStrict(registry, log, scDesc); err != nil {
				return nil, fmt.Errorf("wire registry: script-assets: %w", err)
			}
			// *ScriptAssetsDescriptor satisfies api.Descriptor via the three
			// explicit delegation methods (Name/Enabled/RegisterRoutes), and
			// api.DescriptorProviders via RegisterProviders. Same concrete
			// pointer cast as the generation block above.
			if dp, ok := scDesc.(*scriptassets.ScriptAssetsDescriptor); ok {
				if err := dp.RegisterProviders(pr); err != nil {
					return nil, fmt.Errorf("wire registry: script-assets providers: %w", err)
				}
				log.Info("registered script_assets catalog entry in providers.Registry",
					zap.String("name", "script_assets"),
					zap.Strings("capabilities", []string{"search", "script"}))
			}
		}
		// FREEZE here, after all registrations. (Reviewer Q8 fix.)
		pr.Freeze()
		log.Info("providers.Registry frozen at end of WireRegistry",
			zap.Int("providers", len(pr.All())))

		if wiring.Assets != nil && wiring.Assets.Module != nil {
			log.Info("providers.Registry wired into Assets module via constructor")
		}
		if wiring.YouTubeClip != nil && wiring.YouTubeClip.Handler != nil {
			log.Info("providers.Registry wired into YouTubeClipHandler via constructor (no late-binding needed)")
		}
	}

	return wiring, nil
}

// wireScriptFlow is defined in wire_script.go.
