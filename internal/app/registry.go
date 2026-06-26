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
package app (
	"context"
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	imagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/images"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	artlistadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	stockadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock"
	youtubeadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/youtube"
	searchqueriesuc "github.com/Marcuss-ops/PipelineGen/internal/application/assets/searchqueries"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/api/mediasearch"
	generation "github.com/Marcuss-ops/PipelineGen/internal/application/generation"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	mediasearch "github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"go.uber.org/zap"
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
	// Re-base (June 2026): OutboxHandler has no producer post-PR3 3b
	// (QDRANT-002 retired); nil-tolerated via Server.SetOutboxHandler.
	// MediasearchHandler is populated inline below from QDRANT-004 searchH.
	OutboxHandler      module.InternalOutboxRouter
	MediasearchHandler module.InternalMediaSearchRouter
}

func registerModule(registry *module.Registry, log *zap.Logger, mod module.Module) {
	if err := registry.Register(mod); err != nil {
		log.Warn("failed to register module", zap.String("module", mod.Name()), zap.Error(err))
	}
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
	reconcileSvcAdapter := drivecleanup.NewService()
	registerModule(registry, log, systemapi.NewModule(
		doctorConfigFrom(cfg),
		log,
		toolCheckerAdapter, processRunnerAdapter, dbHealthCheckerAdapter,
		newDriveAdminAdapter(driveUploaderAdapter, log),
		newReconcilerAdapter(reconcileSvcAdapter, log),
	))

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
		registerModule(registry, log, aw.Module)
		wiring.ArtlistSvc = aw
	}

	// ScriptFlow — sources from root.<bundle>.<field>. Extracted into
	// wireScriptFlow (PR7 cleanup, June 2026) to shrink WireRegistry and
	// reuse the canonical engine + memorySvc from AIBundle.
	wireScriptFlow(ctx, cfg, log, root, registry)

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
		registerModule(registry, log, yw.Module)
		wiring.YouTubeClip = yw
	}

	// Jobs, Images, MediaIngest, Drive, Scraper, FullImages, StockPipeline
	// PR2 (June 2026): thin Wire wrappers (Jobs, Images, Drive, Scraper) inlined.
	var imagesHandler *imagesapi.ImagesHandler
	for _, m := range []struct {
		name string
		fn   func() (module.Module, error)
	}{
		{"Jobs", func() (module.Module, error) {
			handler := jobsapi.NewJobsHandler(root.Jobs.Service, log)
			mod := module.NewRouteModule("jobs", func() bool { return true }, "/jobs", handler, log)
			log.Info("created Jobs module")
			return mod, nil
		}},
		{"Images", func() (module.Module, error) {
			var ingestSvc *ingest.Service
			if wiring.MediaIngest != nil {
				ingestSvc = wiring.MediaIngest.Service
			}
			imagesHandler = imagesapi.NewImagesHandler(root.Domains.ImageService, ingestSvc)
			mod := module.NewRouteModule("images", func() bool { return cfg.Features.ImagesEnabled }, "/images", imagesHandler, log)
			log.Info("created Images module")
			return mod, nil
		}},
		{"MediaIngest", func() (module.Module, error) {
			// PR4d-chunk2: WireMediaIngest takes *MediaIngestBundle. PG-011:
			// bundle.DB is now *storage.SQLiteDB (no raw *sql.DB in this layer).
			// PR8: idemHandler installed on POST /api/media/ingest.
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
			w, e := WireMediaIngest(cfg, log, ingestBundle, idemHandler)
			wiring.MediaIngest = w
			if w != nil {
				return w.Module, e
			}
			return nil, e
		}},
		{"Scraper", func() (module.Module, error) {
			handler := assetsapi.NewScraperHandler(cfg.External.NodeScraperDir, processRunnerAdapter)
			mod := module.NewRouteModule("scraper", func() bool { return handler != nil }, "/scraper", handler, log)
			log.Info("created Scraper module")
			return mod, nil
		}},
		{"FullImages", func() (module.Module, error) {
			// PR4d-chunk1: WireFullImages takes ImageService + MediaStore directly.
			w, e := WireFullImages(cfg, log, root.Domains.ImageService, root.Drive.MediaStore)
			wiring.FullImages = w
			if w != nil {
				return w.Module, e
			}
			return nil, e
		}},
		{"StockPipeline", func() (module.Module, error) {
			// PR4d-chunk2: WireStockPipeline takes *StockBundle.
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
			w, e := WireStockPipeline(cfg, log, stockBundle)
			wiring.StockPipeline = w
			if w != nil {
				return w.Module, e
			}
			return nil, e
		}},
	} {
		mod, err := m.fn()
		if err != nil {
			log.Warn("failed to wire module", zap.String("module", m.name), zap.Error(err))
		} else if mod != nil {
			registerModule(registry, log, mod)
		}
	}

	// ── Generation + Channels modules intentionally omitted ──
	// The internal/api/channels and internal/api/generation packages
	// do not exist yet; their wiring was introduced by an origin/main
	// refactor that hasn't been merged into this branch.
		// PR3 (June 2026): Wave 14 close — moved from internal/api/searchqueries/
		// to internal/api/assets/handler_searchqueries.go as SearchQueriesHandler.
		//
		// Wave 14 problem #3 close-out (June 2026): the handler no longer
		// owns the *assets.SearchQueriesRepository. Composition builds
		// the *searchqueriesuc.UseCase from the concrete repo and injects
		// it into the handler — keeping handler = thin transport.
		registerModule(registry, log, module.NewRouteModule(
			"search_queries",
			func() bool { return true },
			"/search-queries",
			assetsapi.NewSearchQueriesHandler(searchqueriesuc.NewUseCase(assets.NewSearchQueriesRepository(root.DB.DB)), log),
			log,
		))
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
		registerModule(registry, log, scriptapi.NewScriptHistoryModule(
			scriptapi.NewScriptHistoryHandler(scriptcore.NewRepositoryAdapter(root.Repos.ScriptsRepo), log),
			log,
			middleware.FeatureFlagChecker("Script", scriptHistoryEnabled),
			scriptHistoryEnabled,
		))
	}
	registerModule(registry, log, module.NewUtilityModule(cfg, log, root.Utility.Utility))

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
	if aw, err := WireAssets(cfg, log, assetsBundle, root.Jobs, voiceoverService, root.Domains.VoiceoverSync, root.Repos.CatalogRepo, maintenanceSvc, root.Search.ProviderRegistry, root.Outbox.Dispatcher); err == nil && aw != nil {
		wiring.Assets = aw
		registerModule(registry, log, aw.Module)
		if maintenanceSvc != nil && aw.DeletionSvc != nil {
			maintenanceSvc.SetDeletionService(aw.DeletionSvc)
			log.Info("injected DeletionService into MaintenanceService")
		}
	}

	// ── QDRANT-004: wire mediasearch handler ─────────────────────────
	// Wires the unified media search API at POST /internal/v1/media/search
	// when Qdrant is enabled and the vector store adapter is available.
	if root.Process.VectorSvc != nil && root.AI != nil && root.AI.OllamaClient != nil {
		vectorStore, _ := root.Process.VectorSvc.(assetsearch.VectorStorePort)
		if vectorStore != nil {
			// Build the VectorSearchPort adapter: OllamaClient for embedding +
			// Qdrant search adapter for vector store operations.
			vectorPort := &mediasearchVectorAdapter{
				embedder: root.AI.OllamaClient,
				store:    vectorStore,
			}
			// MediaReadRepository reads canonical metadata from SQLite.
			readRepo := &mediasearchReadAdapter{clips: root.Repos.ClipsRepo}
			// AssetDeliveryService: use HMAC-signed URLs when secrets are
			// configured, fall back to noop for dev/test.
			deliverySvc := buildMediasearchDeliverySvc(
				cfg.Security.DeliveryHMACSecret,
				cfg.External.VeloxBaseURL,
				cfg.Security.DeliveryReplayWindowSec,
				log,
			)

			searchSvc := mediasearch.NewService(
				vectorPort,
				readRepo,
				deliverySvc,
				mediasearch.Config{},
				mediasearchLogger{sugar: log.Sugar()},
			)
			searchH := mediasearchapi.NewHandler(searchSvc, log)
			// Expose the handler on RegistryWiring before route registration
			// so wire_services.go's AppDeps.MediasearchHandler slot receives
			// the canonical reference (QDRANT-004 surface).
			wiring.MediasearchHandler = searchH
			registerModule(registry, log, module.NewRouteModule(
				"mediasearch",
				func() bool { return cfg.Qdrant.Enabled },
				"/internal/v1/media",
				searchH,
				log,
			))
			log.Info("QDRANT-004: mediasearch handler wired at /internal/v1/media/search")
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
