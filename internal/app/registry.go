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
	"database/sql"
	"fmt"
	"strings"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	channelsapi "github.com/Marcuss-ops/PipelineGen/internal/api/channels"
	contentapi "github.com/Marcuss-ops/PipelineGen/internal/api/content"
	driveapi "github.com/Marcuss-ops/PipelineGen/internal/api/drive"
	imagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/images"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	realtimeapi "github.com/Marcuss-ops/PipelineGen/internal/api/realtime"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	searchqueriesapi "github.com/Marcuss-ops/PipelineGen/internal/api/searchqueries"
	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	artlistadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	stockadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock"
	youtubeadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"

	artlistpkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipresolver"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
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
	registerModule(registry, log, systemapi.NewModule(cfg, log))

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
	if aw, err := WireArtlist(ctx, cfg, log, artlistBundle, root.Process.VectorSvc, root.Outbox.Dispatcher); err != nil {
		log.Warn("failed to wire module", zap.String("module", "Artlist"), zap.Error(err))
	} else {
		registerModule(registry, log, aw.Module)
		wiring.ArtlistSvc = aw
	}

	// ScriptFlow — sources from root.<bundle>.<field>. The previously nullable
	// deps that NewScriptFlowHandler used to accept via post-construction
	// setters (clipSourceBuilder, mediaCurator, harvestSvc) are now constructed
	// inline here and passed by value to the ctor. PR4.E (June 2026) absorbed
	// the 6 setters removed in this wave (clipSourceBuilder, mediaCurator,
	// harvestService, curationClipSourceBuilder, curationJobService,
	// catalogJobService).
	if root.AI != nil && root.AI.ScriptGen != nil && root.Domains.ImageService != nil {
		memoryRepo := gemmamemory.NewRepository(root.DB.DB)
		memorySvc := gemmamemory.NewService(memoryRepo, log)
		scriptsRepoAdapter := scriptcore.NewRepositoryAdapter(root.Repos.ScriptsRepo)
		engine := scriptcore.NewEngine(root.AI.ScriptGen, memorySvc, scriptsRepoAdapter, log)
		batchSvc := scripts.NewBatchService(cfg, log, root.AI.ScriptGen, engine, root.Drive.DocClient, root.Domains.VoiceoverService, scriptsRepoAdapter)
		curationSvc := scripts.NewCurationService(nil, root.Jobs.Service, log)

		// PR4.E: build clipSourceBuilder inline (was in wireScriptFlowExtras + setter).
		var clipSourceBuilder *scripts.ClipSourceBuilder
		if ollamaClient := root.AI.ScriptGen.GetClient(); ollamaClient != nil {
			clipSourceBuilder = scriptcore.NewClipSourceBuilder(root.Repos.ClipsRepo, ollamaClient, log)
			if root.Process.VectorSvc != nil && cfg.Features.CatalogScriptVectorSearch {
				clipSourceBuilder.SetVectorStore(qdrant.NewSearchAdapter(root.Process.VectorSvc))
			}
			if cfg.Reranker.Enabled {
				clipSourceBuilder.SetReranker(reranker.NewClient(reranker.Config{
					Enabled:   cfg.Reranker.Enabled,
					URL:       cfg.Reranker.URL,
					Model:     cfg.Reranker.Model,
					TopK:      cfg.Reranker.TopK,
					TimeoutMs: cfg.Reranker.TimeoutMs,
				}))
			}
		}

		// PR4.E: build mediaCurator inline (was in wireScriptFlowExtras + setter).
		var mediaCurator *scripts.MediaCurator
		if (root.Process.VectorSvc != nil || root.Repos.ClipsRepo != nil) && engine != nil {
			mediaCurator = scripts.NewMediaCurator(qdrant.NewSearchAdapter(root.Process.VectorSvc), cfg.ClipIndexer.ServerURL, root.Repos.ClipsRepo, clipSourceBuilder, engine, log)
		}

		// PR4.E: build harvestSvc inline (was conditional setter call).
		// clipresolver.Service doesn't implement script.AutoHarvestService, so we
		// reuse the JobHarvestService path that was already in use pre-PR4d.
		var harvestSvc scriptapi.AutoHarvestService
		if root.Jobs.Service != nil {
			presetsConfig, _ := artlistpkg.LoadPresets("config/presets.yaml")
			harvestSvc = clipresolver.NewJobHarvestService(root.Jobs.Facade, log, presetsConfig, cfg.Drive.ArtlistFolder())
		}

		// curationJobService + catalogJobService not wired today; passed nil.
		// RegisterJobHandlers already nil-guards them, so the fields stay nil
		// without losing behaviour. A future caller can inject them via ctor.
		handler := scriptapi.NewScriptFlowHandler(
			root.AI.ScriptGen, engine,
			root.Domains.ImageService, root.Domains.RealtimeService, root.Domains.AssocService,
			root.Domains.VoiceoverService, root.Search.AssetTreeService,
			root.Drive.DocClient, root.Drive.DriveUploader, root.Jobs.Facade, scriptsRepoAdapter, memorySvc,
			cfg.Drive.ScriptsGenFolder(), cfg, log,
			batchSvc, curationSvc,
			clipSourceBuilder, mediaCurator, harvestSvc,
			nil, nil,
		)

		genSvc := scripts.NewGenerationService(root.Jobs.Facade, cfg, log)
		mod := module.NewRouteModule(
			"script-flow",
			func() bool { return cfg.Features.ScriptDocsEnabled },
			"/script",
			scriptapi.NewHandler(handler, genSvc),
			log,
			module.WithMiddleware(middleware.ScriptDocsEnabled(cfg)),
		)
		registerModule(registry, log, mod)
	}

	// YouTubeClip (PR4d-chunk2): 4 direct narrow args + ProviderRegistry.
	// ProviderRegistry is not yet populated when WireYouTubeClip runs —
	// the handler's constructor resolves providers lazily so it's fine
	// to pass the empty registry here; it will be populated by the time
	// HTTP requests arrive.
	if yw, err := WireYouTubeClip(cfg, log, root.Domains.YoutubeClipService, root.Jobs.Facade, root.Jobs.Service, root.Repos.ClipsRepo, root.Search.ProviderRegistry); err != nil {
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
			// PR4d-chunk2: WireMediaIngest takes *MediaIngestBundle.
			ingestBundle := &MediaIngestBundle{
				DB:                root.DB.DB,
				Assets:            root.Repos.Assets,
				DriveClient:       root.Drive.DriveClient,
				ImageRepo:         root.Repos.ImageRepo,
				VoiceoverRepo:     root.Repos.VoiceoverRepo,
				ClipsRepo:         root.Repos.ClipsRepo,
				AssetIndexService: root.Search.AssetIndexService,
				PrebuiltService:   root.Domains.IngestService,
			}
			w, e := WireMediaIngest(cfg, log, ingestBundle)
			wiring.MediaIngest = w
			if w != nil {
				return w.Module, e
			}
			return nil, e
		}},
		{"Drive", func() (module.Module, error) {
			var driveUploader *driveup.Uploader
			if root.Drive.DriveClient != nil {
				driveUploader = &driveup.Uploader{Service: root.Drive.DriveClient, Log: log}
			}
			reconcileSvc := drivecleanup.NewService()
			handler := driveapi.NewDriveHandler(reconcileSvc, driveUploader)
			mod := module.NewRouteModule("drive", func() bool { return cfg.Features.DriveEnabled }, "/drive", handler, log)
			log.Info("created Drive module")
			return mod, nil
		}},
		{"Scraper", func() (module.Module, error) {
			handler := assetsapi.NewScraperHandler(cfg.External.NodeScraperDir)
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

	if root.Domains != nil && root.Domains.RealtimeService != nil {
		realtimeEnabled := cfg != nil && cfg.VectorSearch.RealtimeEnabled
		registerModule(registry, log, module.NewRouteModule(
			"realtime",
			func() bool { return root.Domains.RealtimeService != nil && realtimeEnabled },
			"",
			realtimeapi.NewMatchHandler(root.Domains.RealtimeService, log),
			log,
		))
	}
	if root.Domains != nil && root.Domains.BooksService != nil {
		registerModule(registry, log, module.NewRouteModule(
			"books",
			func() bool { return cfg.Books.Enabled },
			"/books",
			contentapi.NewBooksHandler(root.Domains.BooksService, root.Jobs.Facade, log),
			log,
		))
	}
	if root.Domains != nil && root.Domains.LessonsService != nil {
		registerModule(registry, log, module.NewRouteModule(
			"lessons",
			func() bool { return cfg.Lessons.Enabled },
			"/lessons",
			contentapi.NewLessonsHandler(root.Domains.LessonsService, root.Jobs.Facade, log),
			log,
		))
	}
	if root.DB != nil && root.DB.DB != nil {
		registerModule(registry, log, module.NewRouteModule(
			"channels",
			func() bool { return true },
			"/channels",
			channelsapi.NewChannelsHandler(assets.NewChannelsRepository(root.DB.DB), log),
			log,
		))
		registerModule(registry, log, module.NewRouteModule(
			"search_queries",
			func() bool { return true },
			"/search-queries",
			searchqueriesapi.NewSearchqueriesHandler(assets.NewSearchQueriesRepository(root.DB.DB), log),
			log,
		))
	}

	if wiring.MediaIngest != nil {
		log.Info("MediaIngest module wired (service pre-built via BuildDomainBundle, no late-binding needed)")
	}
	if root.Repos != nil && root.Repos.ScriptsRepo != nil {
		registerModule(registry, log, scriptapi.NewScriptHistoryModule(cfg, log, scriptapi.NewScriptHistoryHandler(scriptcore.NewRepositoryAdapter(root.Repos.ScriptsRepo), log)))
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
		ClipsRepo:          root.Repos.ClipsRepo,
		VoiceoverRepo:      root.Repos.VoiceoverRepo,
		ImageRepo:          root.Repos.ImageRepo,
		Assets:             root.Repos.Assets,
		DriveClient:        root.Drive.DriveClient,
		AssetTreeService:   root.Search.AssetTreeService,
		AssetIndexService:  root.Search.AssetIndexService,
		MediaProcessor:     root.Process.MediaProcessor,
		CatalogSyncService: root.Sync.CatalogSync,
		ClipIndexerService: root.Process.ClipIndexerService,
	}
	if aw, err := WireAssets(cfg, log, assetsBundle, root.Process.VectorSvc, root.Jobs, voiceoverService, root.Domains.VoiceoverSync, root.Domains.RealtimeService, root.Repos.CatalogRepo, maintenanceSvc, root.Search.ProviderRegistry); err == nil && aw != nil {
		wiring.Assets = aw
		registerModule(registry, log, aw.Module)
		if aw.NewAssetsModule != nil {
			registerModule(registry, log, aw.NewAssetsModule)
			log.Info("registered new thin-transport Assets module alongside legacy SourcesModule")
		}
		if maintenanceSvc != nil && aw.DeletionSvc != nil {
			maintenanceSvc.SetDeletionService(aw.DeletionSvc)
			log.Info("injected DeletionService into MaintenanceService")
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

		if wiring.Assets != nil && wiring.Assets.Handler != nil {
			log.Info("providers.Registry wired into SourcesHandler via constructor (no late-binding needed)")
		}
		if wiring.YouTubeClip != nil && wiring.YouTubeClip.Handler != nil {
			log.Info("providers.Registry wired into YouTubeClipHandler via constructor (no late-binding needed)")
		}
	}

	return wiring, nil
}

func initAssetServices(dbs *databases, log *zap.Logger) (*assetindex.Service, *assettree.Service, error) {
	assetIndexRepo := assetindex.NewRepository(dbs.main.DB)
	assetIndexService := assetindex.NewService(assetIndexRepo)
	assetTreeRepo, err := assets.NewAssetTreeRepository(dbs.main.DB, log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize asset tree repository: %w", err)
	}
	assetTreeService := assettree.NewService(assetTreeRepo, log)
	return assetIndexService, assetTreeService, nil
}

type DriveDestinations struct {
	MediaRoot, VideoAIRoot, SoundEffectsRoot, imagesFolder, videoAIFolder string
}

func (d *DriveDestinations) RootFolder() string    { return d.MediaRoot }
func (d *DriveDestinations) ImagesFolder() string  { return d.imagesFolder }
func (d *DriveDestinations) VideoAIFolder() string { return d.videoAIFolder }

func resolveRuntimeDestinations(ctx context.Context, db *sql.DB, driveClient *gdrive.Service, cfg *config.Config, log *zap.Logger) *DriveDestinations {
	return &DriveDestinations{MediaRoot: cfg.Drive.RootFolder(), VideoAIRoot: cfg.Drive.VideoAIRootFolder, SoundEffectsRoot: cfg.Drive.SoundEffectsRootFolder, imagesFolder: cfg.Drive.ImagesFolder(), videoAIFolder: cfg.Drive.VideoAIFolder()}
}

func configOnlyDestinations(cfg *config.Config) *DriveDestinations {
	return &DriveDestinations{MediaRoot: cfg.Drive.RootFolder(), VideoAIRoot: cfg.Drive.VideoAIRootFolder, SoundEffectsRoot: cfg.Drive.SoundEffectsRootFolder, imagesFolder: cfg.Drive.ImagesFolder(), videoAIFolder: cfg.Drive.VideoAIFolder()}
}

func initMediaProcessor(cfg *config.Config, db *sql.DB, assetsRepo asset.Repository, querySvc *asset.Service, locations asset.LocationRepository, processing asset.ProcessingRepository, log *zap.Logger, driveUploader *driveup.Uploader) asset.Processor {
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	httpDL := downloader.NewHTTPDownloader(5 * time.Minute)
	ffmpegProc := ffmpeg.NewFromConfig(cfg)
	clipsRegistry := artifacts.NewClipsRegistry(db, assetsRepo, querySvc, locations, processing)
	return processor.NewProcessor(ytDLPDownloader, httpDL, ffmpegProc, log, processor.ProcessorConfig{DataDir: cfg.Storage.DataDir, TempDir: cfg.Storage.TempDir, VideoCfg: ffmpeg.DefaultNormalizeOptions(cfg), ScraperServerURL: cfg.External.ArtlistScraperServerURL, EmbeddingServerURL: cfg.ClipIndexer.ServerURL}, clipsRegistry, driveUploader)
}

func buildSyncTargets(cfg *config.Config, clipsOnlyRepo *assets.ClipsRepository, clipsRepo *assets.ClipsRepository, artlistRepo *assets.ClipsRepository) []catalogsync.Target {
	targets := []catalogsync.Target{
		{Name: "stock", RootFolderID: cfg.Drive.StockFolder(), Source: "stock", MediaType: "stock", Repo: clipsRepo},
		{Name: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Source: "youtube", MediaType: "clip", Repo: clipsOnlyRepo},
		{Name: "artlist", RootFolderID: cfg.Drive.ArtlistFolder(), Source: "artlist", MediaType: "artlist", Repo: artlistRepo},
	}
	if videoAIRoot := cfg.Drive.VideoAIFolder(); videoAIRoot != "" {
		targets = append(targets, catalogsync.Target{Name: "videoai", RootFolderID: videoAIRoot, Source: "videoai", MediaType: "image", Repo: artlistRepo})
	}
	return targets
}

func ensureStyleDriveFolders(ctx context.Context, uploader *driveup.Uploader, rootID string, styleRegistry *generation.StyleRegistry, log *zap.Logger) {
	if uploader == nil || strings.TrimSpace(rootID) == "" || styleRegistry == nil {
		return
	}
	for _, st := range styleRegistry.List() {
		name := strings.TrimSpace(st.Name)
		if name == "" {
			continue
		}
		if _, err := uploader.GetOrCreateFolder(ctx, name, rootID); err != nil && log != nil {
			log.Warn("failed to pre-create style folder", zap.String("style", name), zap.String("root_id", rootID), zap.Error(err))
		}
	}
}
