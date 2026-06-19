package app

import (
	scraperapi "github.com/Marcuss-ops/PipelineGen/internal/api/scraper"
	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"go.uber.org/zap"
	"context"
	booksapi "github.com/Marcuss-ops/PipelineGen/internal/api/books"
	batchpkg "github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/batch"
	curationpkg "github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/curation"
	channelsapi "github.com/Marcuss-ops/PipelineGen/internal/api/channels"
	lessonsapi "github.com/Marcuss-ops/PipelineGen/internal/api/lessons"
	realtimeapi "github.com/Marcuss-ops/PipelineGen/internal/api/realtime"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	sourcesapi "github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	"github.com/Marcuss-ops/PipelineGen/internal/core/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipresolver"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	channelsrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/channels"
	searchqueriesrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/searchqueries"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts/gemmamemory"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/generate"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/content/mediacurator"
	"time"
	sources "github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	artsources "github.com/Marcuss-ops/PipelineGen/internal/api/sources/artlist"
	ytsources "github.com/Marcuss-ops/PipelineGen/internal/api/sources/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/core/destination"
	"github.com/Marcuss-ops/PipelineGen/internal/core/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipcatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/ontology"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/storage"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/sources/artlist"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	svcjobs "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	pf "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
	"fmt"
	sourcespkg "github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/foldermemory"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/media/voiceoversync"
	assettreerepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	driveapi "github.com/Marcuss-ops/PipelineGen/internal/api/drive"
	"database/sql"
	gdrive "google.golang.org/api/drive/v3"
	fullimageshandler "github.com/Marcuss-ops/PipelineGen/internal/api/fullimages"
	"github.com/Marcuss-ops/PipelineGen/internal/media/fullimages"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	imagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/images"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	"strings"
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/mediaingest"
	imgreg "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/media/ingest"
	voingsvc "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/media/mediaasset"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/media/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
)

// SystemWiring holds the System module wiring
type SystemWiring struct {
	Module module.Module
}

// ScraperWiring holds the Scraper module wiring
type ScraperWiring struct {
	Handler *scraperapi.ScraperHandler
	Module  module.Module
}

// WireScraper creates the Scraper handler and module
func WireScraper(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ScraperWiring, error) {
	handler := scraperapi.NewScraperHandler(cfg.External.NodeScraperDir)
	mod := scraperapi.NewModule(log, handler)
	log.Info("created Scraper module")

	return &ScraperWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}

// WireSystem creates the System handler and module
func WireSystem(
	cfg *config.Config,
	log *zap.Logger,
) *SystemWiring {
	mod := systemapi.NewModule(cfg, log)
	log.Info("created System module")

	return &SystemWiring{
		Module: mod,
	}
}
// RegistryWiring holds the registry and all wired modules.
type RegistryWiring struct {
	Registry      *module.Registry
	System        *SystemWiring
	ArtlistSvc    *ArtlistWiring
	YouTubeClip   *YouTubeClipWiring
	Jobs          *JobsWiring
	Images        *ImagesWiring
	MediaIngest   *MediaIngestWiring
	Drive         *DriveWiring
	Scraper       *ScraperWiring
	Assets        *AssetsWiring
	FullImages    *FullImagesWiring
	StockPipeline *StockPipelineWiring
}

// registerModule is a helper to safely register a module and log on error.
func registerModule(registry *module.Registry, log *zap.Logger, mod module.Module) {
	if err := registry.Register(mod); err != nil {
		log.Warn("failed to register module", zap.String("module", mod.Name()), zap.Error(err))
	}
}

// WireRegistry creates and populates the module registry with all modules.
func WireRegistry(
	ctx context.Context,
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*RegistryWiring, error) {
	registry := module.NewRegistry()
	wiring := &RegistryWiring{Registry: registry}

	// ── System ─────────────────────────────────────────────────────────
	systemWiring := WireSystem(cfg, log)
	registerModule(registry, log, systemWiring.Module)
	wiring.System = systemWiring

	// ── Artlist ────────────────────────────────────────────────────────
	artlistWiring, err := WireArtlist(ctx, cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "Artlist"), zap.Error(err))
	} else {
		registerModule(registry, log, artlistWiring.Module)
		wiring.ArtlistSvc = artlistWiring
	}

	// ── ScriptFlow ─────────────────────────────────────────────────────
	if coreDeps.ScriptGen != nil && coreDeps.ImageService != nil {
		memoryRepo := gemmamemory.NewRepository(coreDeps.DB.DB)
		memorySvc := gemmamemory.NewService(memoryRepo, log)
		engine := scriptcore.NewEngine(coreDeps.ScriptGen, memorySvc, coreDeps.ScriptsRepo, log)
		handler := scriptapi.NewScriptFlowHandler(
			coreDeps.ScriptGen, engine, coreDeps.ImageService, coreDeps.RealtimeService,
			coreDeps.AssocService, coreDeps.VoiceoverService, coreDeps.AssetTreeService,
			coreDeps.DocClient,
			coreDeps.DriveUploader, coreDeps.JobsService, coreDeps.ScriptsRepo, memorySvc,
			cfg.Drive.ScriptsGenFolder(), cfg, log,
		)

		batchSvc := batchpkg.NewBatchService(cfg, log, coreDeps.ScriptGen, engine, coreDeps.DocClient, coreDeps.VoiceoverService, coreDeps.ScriptsRepo)
		handler.SetBatchService(batchSvc)

		curationSvc := curationpkg.NewCurationService(nil, coreDeps.JobsService, log)
		handler.SetCurationService(curationSvc)

		wireScriptFlowExtras(handler, coreDeps.ScriptGen.GetClient(), coreDeps.VectorStore,
			coreDeps.ClipsRepo, engine, cfg, log)

		if coreDeps.JobsService != nil {
			presetsConfig, _ := artlist.LoadPresets("config/presets.yaml")
			harvestSvc := clipresolver.NewJobHarvestService(coreDeps.JobsService, log, presetsConfig, cfg.Drive.ArtlistFolder())
			handler.SetHarvestService(harvestSvc)
		}
		genSvc := generate.NewGenerationService(coreDeps.JobsService, cfg, log)
		mod := scriptapi.NewModule(cfg, log, scriptapi.NewHandler(handler, genSvc))
		registerModule(registry, log, mod)
	}

	// ── YouTubeClip ────────────────────────────────────────────────────
	ytWiring, err := WireYouTubeClip(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "YouTubeClip"), zap.Error(err))
	} else {
		registerModule(registry, log, ytWiring.Module)
		wiring.YouTubeClip = ytWiring
	}

	// ── Jobs ───────────────────────────────────────────────────────────
	jobsWiring, err := WireJobs(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "Jobs"), zap.Error(err))
	} else {
		registerModule(registry, log, jobsWiring.Module)
		wiring.Jobs = jobsWiring
	}

	// ── Images ─────────────────────────────────────────────────────────
	imagesWiring, err := WireImages(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "Images"), zap.Error(err))
	} else {
		registerModule(registry, log, imagesWiring.Module)
		wiring.Images = imagesWiring
	}

	// ── MediaIngest ────────────────────────────────────────────────────
	mediaIngestWiring, err := WireMediaIngest(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "MediaIngest"), zap.Error(err))
	} else if mediaIngestWiring != nil {
		registerModule(registry, log, mediaIngestWiring.Module)
		wiring.MediaIngest = mediaIngestWiring
	}

	// ── Drive ──────────────────────────────────────────────────────────
	driveWiring, err := WireDrive(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "Drive"), zap.Error(err))
	} else {
		registerModule(registry, log, driveWiring.Module)
		wiring.Drive = driveWiring
	}

	// ── Scraper ────────────────────────────────────────────────────────
	scraperWiring, err := WireScraper(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "Scraper"), zap.Error(err))
	} else {
		registerModule(registry, log, scraperWiring.Module)
		wiring.Scraper = scraperWiring
	}

	// ── FullImages ─────────────────────────────────────────────────────
	fullImagesWiring, err := WireFullImages(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "FullImages"), zap.Error(err))
	} else if fullImagesWiring != nil {
		registerModule(registry, log, fullImagesWiring.Module)
		wiring.FullImages = fullImagesWiring
	}

	// ── StockPipeline ──────────────────────────────────────────────────
	stockWiring, err := WireStockPipeline(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "StockPipeline"), zap.Error(err))
	} else if stockWiring != nil {
		registerModule(registry, log, stockWiring.Module)
		wiring.StockPipeline = stockWiring
	}

	// ── Realtime ───────────────────────────────────────────────────────
	if coreDeps.RealtimeService != nil {
		handler := realtimeapi.NewMatchHandler(coreDeps.RealtimeService, log)
		mod := sourcesapi.NewRealtimeModule(cfg, log, handler)
		registerModule(registry, log, mod)
	}

	// ── Books ──────────────────────────────────────────────────────────
	if coreDeps.BooksService != nil {
		handler := booksapi.NewBooksHandler(coreDeps.BooksService, coreDeps.JobsService, log)
		mod := booksapi.NewModule(cfg, log, handler)
		registerModule(registry, log, mod)
	}

	// ── Lessons ────────────────────────────────────────────────────────
	if coreDeps.LessonsService != nil {
		handler := lessonsapi.NewLessonsHandler(coreDeps.LessonsService, coreDeps.JobsService, log)
		mod := lessonsapi.NewModule(cfg, log, handler)
		registerModule(registry, log, mod)
	}

	// ── Channels ───────────────────────────────────────────────────────
	if coreDeps.DB != nil && coreDeps.DB.DB != nil {
		repo := channelsrepo.NewRepository(coreDeps.DB.DB)
		mod := channelsapi.NewModule(log, repo)
		registerModule(registry, log, mod)
	}

	// ── SearchQueries ──────────────────────────────────────────────────
	if coreDeps.DB != nil && coreDeps.DB.DB != nil {
		repo := searchqueriesrepo.NewRepository(coreDeps.DB.DB)
		mod := sourcesapi.NewSearchQueriesModule(log, repo)
		registerModule(registry, log, mod)
	}

	// ── Post-wiring cross-injections ───────────────────────────────────
	if wiring.Images != nil && wiring.MediaIngest != nil {
		if wiring.Images.Handler != nil {
			wiring.Images.Handler.SetIngestService(wiring.MediaIngest.Service)
			log.Info("injected MediaIngest service into ImagesHandler")
		}
		if coreDeps.ImageService != nil {
			coreDeps.ImageService.SetIngestService(wiring.MediaIngest.Service)
			log.Info("injected MediaIngest service into ImagesService")
		}
	}

	// ── ScriptHistory (dynamic module) ─────────────────────────────────
	if coreDeps.ScriptsRepo != nil {
		registerModule(registry, log, scriptapi.NewScriptHistoryModule(
			cfg, log, scriptapi.NewScriptHistoryHandler(coreDeps.ScriptsRepo, log),
		))
	}

	registerModule(registry, log, module.NewUtilityModule(cfg, log, coreDeps.Utility))

	// ── Maintenance Service ────────────────────────────────────────────
	// Uses a single DB reference (the double-DB bug is fixed).
	maintenanceSvc := maintenance.NewService(cfg, log,
		coreDeps.AssetIndexService, coreDeps.AssetTreeService,
		coreDeps.DeletionService, coreDeps.JobsService, coreDeps.DB.DB)
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		log.Warn("failed to register maintenance handler", zap.Error(err))
	}
	coreDeps.MaintenanceService = maintenanceSvc

	// ── Artlist / YouTube / Voiceover wiring ───────────────────────────
	var artlistService *artlist.Service
	if wiring.ArtlistSvc != nil {
		artlistService = wiring.ArtlistSvc.Service
	}

	var youtubeClipService *youtube.Service
	if wiring.YouTubeClip != nil {
		youtubeClipService = wiring.YouTubeClip.Service
	}

	var voiceoverService *voiceover.Service
	if coreDeps.VoiceoverService != nil {
		voiceoverService = coreDeps.VoiceoverService
	}

	// ── Assets ─────────────────────────────────────────────────────────
	if assetsWiring, err := WireAssets(
		cfg, log, coreDeps, artlistService, youtubeClipService,
		voiceoverService, coreDeps.VoiceoverSync, coreDeps.JobsService,
		coreDeps.CatalogRepo, coreDeps.AssetIndexService, maintenanceSvc,
	); err == nil && assetsWiring != nil {
		wiring.Assets = assetsWiring
		registerModule(registry, log, assetsWiring.Module)
		coreDeps.DeletionService = assetsWiring.DeletionSvc

		if maintenanceSvc != nil && assetsWiring.DeletionSvc != nil {
			maintenanceSvc.SetDeletionService(assetsWiring.DeletionSvc)
			log.Info("injected DeletionService into MaintenanceService")
		}
	}

	return wiring, nil
}
// wireScriptFlowExtras wires the optional clip-source builder and media curator
// into a ScriptFlowHandler.  This is the single place where the dependency
// checks, logging, and service construction live so that both the registry
// path (HTTP routes) and the compose-integration path (job handlers) stay in
// sync.
func wireScriptFlowExtras(
	handler *scriptpkg.ScriptFlowHandler,
	ollamaClient *client.Client,
	vectorStore *vectorstore.Service,
	clipsOnlyRepo *clips.Repository,
	engine *scriptcore.Engine,
	cfg *config.Config,
	log *zap.Logger,
) {
	if ollamaClient == nil {
		log.Info("ollama client not available, skipping clip source builder wiring")
		return
	}

	clipSourceBuilder := scriptcore.NewClipSourceBuilder(clipsOnlyRepo, ollamaClient, log)
	if vectorStore != nil && cfg.Features.CatalogScriptVectorSearch {
		clipSourceBuilder.SetVectorStore(vectorStore)
		log.Info("vector store wired into clip source builder for semantic catalog search")
	} else if vectorStore != nil && !cfg.Features.CatalogScriptVectorSearch {
		log.Info("vector store available but catalog script vector search disabled via config")
	}
	if cfg.Reranker.Enabled {
		rerankerCli := reranker.NewClient(reranker.Config{
			Enabled:   cfg.Reranker.Enabled,
			URL:       cfg.Reranker.URL,
			Model:     cfg.Reranker.Model,
			TopK:      cfg.Reranker.TopK,
			TimeoutMs: cfg.Reranker.TimeoutMs,
		})
		clipSourceBuilder.SetReranker(rerankerCli)
		log.Info("reranker wired into clip source builder for catalog result reordering")
	}
	handler.SetClipSourceBuilder(clipSourceBuilder)
	log.Info("clip source builder initialized for Clip→Script and Catalog→Script generation")

	// Wire ClipSourceBuilder into the curation service for GenerateFromCatalog endpoint.
	// The CurationService is created before wireScriptFlowExtras with nil ClipSourceBuilder
	// (late-binding pattern — same as how it's done on ScriptFlowHandler).
	handler.SetCurationClipSourceBuilder(clipSourceBuilder)

	if (vectorStore != nil || clipsOnlyRepo != nil) && engine != nil {
		embedderURL := cfg.ClipIndexer.ServerURL
		mediaCurator := mediacurator.NewService(vectorStore, embedderURL, clipsOnlyRepo, clipSourceBuilder, engine, log)
		handler.SetMediaCurator(mediaCurator)
		log.Info("media curator initialized",
			zap.String("embedder_url", embedderURL))
	} else {
		log.Warn("media curator not initialized: missing dependencies",
			zap.Bool("vectorstore", vectorStore != nil),
			zap.Bool("engine", engine != nil))
	}
}
// ArtlistWiring holds the Artlist module wiring
type ArtlistWiring struct {
	Handler *artsources.ArtlistHandler
	Module  module.Module
	Service *artlistPkg.Service
}

// WireArtlist creates the Artlist service, handler, and module
func WireArtlist(
	ctx context.Context,
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ArtlistWiring, error) {
	// 1. Lifecycle
	artlistLifecycle := wireArtlistLifecycle(coreDeps, log)

	// 2. Catalog & Indexer
	clipCatalogRepo, clipIndexerSvc := wireArtlistCatalog(ctx, cfg, coreDeps, log)

	// 3. Resolvers
	assetDestResolver := wireAssetDestinationResolver(cfg, coreDeps, log)

	// Load presets early
	presetsConfig, err := artlistPkg.LoadPresets("config/artlist_presets.yaml")
	if err != nil {
		log.Warn("failed to load artlist presets, using defaults", zap.Error(err))
	}

	// 4. Service
	artlistSvc, err := wireArtlistService(cfg, coreDeps, artlistLifecycle, assetDestResolver, clipIndexerSvc, log)
	if err != nil {
		log.Warn("Failed to create Artlist service", zap.Error(err))
		return nil, err
	}

	// 4b. Wire SemanticEnricher into artlist service
	if artlistSvc != nil && coreDeps.ClipsRepo != nil && clipIndexerSvc != nil {
		metaWriter := semantic.NewMetadataWriter(
			cfg.Paths.PythonScriptsDir,
			cfg.Storage.TempPath(),
			cfg.External.OllamaURL,
			cfg.External.OllamaModel,
			log,
		)
		enricher := artlistPkg.NewSemanticEnricher(coreDeps.ClipsRepo, clipIndexerSvc, metaWriter, coreDeps.DriveUploader, log)
		artlistSvc.SetSemanticEnricher(enricher)
		log.Info("wired semantic enricher into artlist service")
	}

	// 5. Clip Resolver
	clipResolver := wireClipResolver(cfg, coreDeps, clipCatalogRepo, presetsConfig, log)
	if clipResolver != nil {
		coreDeps.ClipResolver = clipResolver
	}

	// 6. Handler
	handler := wireArtlistHandler(cfg, artlistSvc, coreDeps, clipResolver, log)

	// 7. Module
	var mod module.Module
	if artlistSvc != nil && handler != nil {
		mod = sources.NewArtlistModule(cfg, log, artlistSvc, handler)
		log.Info("created Artlist module")
	}

	return &ArtlistWiring{
		Handler: handler,
		Module:  mod,
		Service: artlistSvc,
	}, nil
}

func wireArtlistHandler(
	cfg *config.Config,
	artlistSvc *artlistPkg.Service,
	coreDeps *CoreDeps,
	clipResolver *clipresolver.Service,
	log *zap.Logger,
) *artsources.ArtlistHandler {
	if artlistSvc == nil {
		return nil
	}
	return artsources.NewArtlistHandler(
		artlistSvc,
		coreDeps.CatalogSyncService,
		coreDeps.JobsService,
		clipResolver,
		"node-scraper",
		log,
		cfg,
	)
}

func wireArtlistLifecycle(coreDeps *CoreDeps, log *zap.Logger) *lifecycle.Service {
	clipsRegistry := artifacts.NewClipsRegistry(
		coreDeps.DB.DB,
		coreDeps.Assets.Repository(),
		coreDeps.Assets,
		coreDeps.Assets.LocationRepository(),
		coreDeps.Assets.ProcessingRepository(),
	)
	return NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipsRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
	}, log)
}

func wireAssetDestinationResolver(cfg *config.Config, coreDeps *CoreDeps, log *zap.Logger) destination.Resolver {
	if coreDeps.DriveClient != nil {
		storageResolver := storage.NewResolver(
			storage.MediaRoot(cfg.Storage.MediaPath()),
			storage.DriveRoot(cfg.Drive.RootFolder()),
		)
		mediaStore := storage.NewStore(storageResolver, &driveutil.Uploader{Service: coreDeps.DriveClient, Log: log}, cfg.Drive.RootFolder(), "", "", cfg.Drive.SoundEffectsFolder(), log)
		return storage.NewDestinationResolver(mediaStore)
	}
	return nil
}

func wireClipResolver(cfg *config.Config, coreDeps *CoreDeps, clipCatalogRepo *clipcatalog.Repository, presetsConfig *artlistPkg.PresetsConfig, log *zap.Logger) *clipresolver.Service {
	if clipCatalogRepo == nil {
		return nil
	}

	var harvestSvc clipresolver.ArtlistHarvestService
	if coreDeps.JobsService != nil {
		harvestSvc = clipresolver.NewJobHarvestService(coreDeps.JobsService, log, presetsConfig, cfg.Drive.ArtlistFolder())
	}

	matchingCfg, err := pf.LoadMatchingConfig("config/matching.yaml")
	if err != nil {
		log.Warn("failed to load matching config, using defaults", zap.Error(err))
	}

	// Load ontology registry
	ontologyReg, err := ontology.LoadRegistry("config/ontology.yaml")
	var ontologyScorer clipresolver.OntologyScorer
	if err != nil {
		log.Warn("failed to load ontology registry", zap.Error(err))
	} else {
		ontologyScorer = ontology.NewScorer(ontologyReg)
	}

	// Create embedding provider (points to the Python server started by clipindexer)
	embedServerURL := cfg.ClipIndexer.ServerURL
	if embedServerURL == "" {
		embedServerURL = "http://127.0.0.1:8001"
	}
	embedProvider := clipresolver.NewPythonEmbeddingProvider(embedServerURL)

	// Build vector store searcher adapter if vector store is configured
	var vectorStoreSearcher clipresolver.VectorStoreSearcher
	if coreDeps.VectorStore != nil && coreDeps.VectorStore.Enabled() {
		vectorStoreSearcher = clipresolver.NewVectorStoreAdapter(coreDeps.VectorStore)
		log.Info("vector store searcher enabled for clip resolver")
	}

	// Build map of prioritized repositories
	repos := make(map[string]*clipcatalog.Repository)

	// 1. Stock database (highest priority)
	if coreDeps.ClipsRepo != nil && coreDeps.ClipsRepo.DB() != nil {
		repos["stock"] = clipcatalog.NewRepository(coreDeps.ClipsRepo.DB(), log)
		repos["stock"].SetSource("stock")
	}

	// 2. YouTube clips database
	if coreDeps.DB != nil && coreDeps.DB.DB != nil {
		repos["youtube"] = clipcatalog.NewRepository(coreDeps.DB.DB, log)
		repos["youtube"].SetSource("youtube")
	}

	// 3. Artlist database (fallback)
	repos["artlist"] = clipCatalogRepo
	repos["artlist"].SetSource("artlist")

	// Create LLM decision service for final clip evaluation
	llmCfg := clipresolver.DefaultLLMDecisionConfig()
	llmCfg.Model = cfg.External.OllamaModel
	llmCfg.Timeout = time.Duration(cfg.External.OllamaTimeoutSeconds) * time.Second

	var llmDecision *clipresolver.LLMDecisionService
	ollamaClient := client.NewClient(cfg.External.OllamaURL, cfg.External.OllamaModel, cfg.External.OllamaTimeoutSeconds)
	if ollamaClient != nil {
		llmDecision = clipresolver.NewLLMDecisionService(ollamaClient, llmCfg, log)
		log.Info("LLM decision layer enabled for clip resolver",
			zap.String("model", llmCfg.Model),
			zap.Int("top_k", llmCfg.TopK))
	}

	return clipresolver.NewService(repos, harvestSvc, embedProvider, ontologyScorer, matchingCfg, vectorStoreSearcher, llmDecision)
}

func wireArtlistService(
	cfg *config.Config,
	coreDeps *CoreDeps,
	artlistLifecycle *lifecycle.Service,
	assetDestResolver destination.Resolver,
	clipIndexerSvc *clipindexer.Service,
	log *zap.Logger,
) (*artlistPkg.Service, error) {
	artlistSvc, err := artlistPkg.NewService(
		cfg,
		coreDeps.DB.DB,
		coreDeps.DB.DB,
		coreDeps.ClipsRepo,
		coreDeps.MediaProcessor,
		artlistLifecycle,
		assetDestResolver,
		clipIndexerSvc,
		coreDeps.JobsService,
		coreDeps.DriveClient,
		coreDeps.Assets.ProcessingRepository(),
		coreDeps.Assets.VersionRepository(),
		coreDeps.Assets.LocationRepository(),
		log,
	)

	if err != nil {
		return nil, err
	}

	// Register artlist job handler
	if artlistSvc != nil && coreDeps.JobsService != nil {
		coreDeps.JobsService.RegisterHandler(svcjobs.JobTypeArtlistRun, artlistSvc.HandleJob)
		log.Info("registered artlist job handler")
	}

	return artlistSvc, nil
}
func wireArtlistCatalog(ctx context.Context, cfg *config.Config, coreDeps *CoreDeps, log *zap.Logger) (*clipcatalog.Repository, *clipindexer.Service) {
	if coreDeps.ClipIndexerService != nil {
		clipCatalogRepo := clipcatalog.NewRepository(coreDeps.DB.DB, log)
		return clipCatalogRepo, coreDeps.ClipIndexerService
	}

	if coreDeps.DB != nil && coreDeps.DB.DB != nil {
		if err := clipcatalog.EnsureSchema(ctx, coreDeps.DB.DB, log); err != nil {
			log.Warn("failed to ensure clipcatalog schema", zap.Error(err))
		}
	}

	clipCatalogRepo := clipcatalog.NewRepository(coreDeps.DB.DB, log)

	clipIndexerSvc := clipindexer.NewService(&clipindexer.Config{
		Enabled:               cfg.ClipIndexer.Enabled,
		ServerURL:             cfg.ClipIndexer.ServerURL,
		ScriptPath:            cfg.ClipIndexer.ScriptPath,
		PythonBin:             cfg.ClipIndexer.PythonBin,
		AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist,
		DBPath:                coreDeps.DB.Path(),
	}, coreDeps.DB.DB, coreDeps.DB.Path(), log)

	// Start background embedding server and watchdog
	if err := clipIndexerSvc.StartServer(ctx); err != nil {
		log.Warn("failed to start embedding server", zap.Error(err))
	} else {
		clipIndexerSvc.StartWatchdog(ctx)
	}

	return clipCatalogRepo, clipIndexerSvc
}
func initAssetServices(dbs *databases, log *zap.Logger) (*assetindex.Service, *assettree.Service, error) {
	// Asset index service
	assetIndexRepo := assetindex.NewRepository(dbs.main.DB)
	assetIndexService := assetindex.NewService(assetIndexRepo)
	log.Info("asset index service initialized", zap.String("db", "assets.db.sqlite"))

	// Asset tree service
	assetTreeRepo, err := assettreerepo.NewRepository(dbs.main.DB, log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize asset tree repository: %w", err)
	}
	assetTreeService := assettree.NewService(assetTreeRepo, log)
	log.Info("asset tree service initialized")

	return assetIndexService, assetTreeService, nil
}

// AssetsWiring holds the Assets module wiring
type AssetsWiring struct {
	Handler     *sourcespkg.SourcesHandler
	Module      module.Module
	DeletionSvc *media.DeletionService
}

// WireAssets creates the unified Assets handler and module
func WireAssets(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
	artlistSvc *artlist.Service,
	youtubeSvc *youtube.Service,
	voiceoverSvc *voiceover.Service,
	voiceoverSync *voiceoversync.Service,
	jobsSvc *jobservice.Service,
	catalogRepo *catalog.Repository,
	assetIndexSvc *assetindex.Service,
	maintenanceSvc *maintenance.Service,
) (*AssetsWiring, error) {
	// Create folder memory service
	folderMemSvc := foldermemory.NewService(log, coreDeps.ClipsRepo)

	// Create drive uploader
	var driveUploader *drive.Uploader
	if coreDeps.DriveClient != nil {
		driveUploader = &drive.Uploader{Service: coreDeps.DriveClient, Log: log}
	}

	// Create drive cleanup service
	var driveCleanupSvc *drivecleanup.Service
	if driveUploader != nil {
		driveCleanupSvc = drivecleanup.NewService()
	}

	// Create deletion service
	deletionSvc := media.NewDeletionService(
		coreDeps.ClipsRepo,
		coreDeps.ClipsRepo,
		coreDeps.ClipsRepo,
		coreDeps.VoiceoverRepo,
		coreDeps.ImageRepo,
		driveUploader,
		coreDeps.AssetTreeService,
		coreDeps.AssetIndexService,
		log,
	)

	handler := sourcespkg.NewSourcesHandler(
		cfg,
		artlistSvc,
		youtubeSvc,
		voiceoverSvc,
		voiceoverSync,
		jobsSvc,
		catalogRepo,
		assetIndexSvc,
		coreDeps.ClipsRepo,
		coreDeps.ClipsRepo,
		coreDeps.ClipsRepo,
		driveCleanupSvc,
		folderMemSvc,
		coreDeps.AssetTreeService,
		driveUploader,
		coreDeps.MediaProcessor,
		deletionSvc,
		coreDeps.CatalogSyncService,
		maintenanceSvc,
		log,
	)

	// Add voiceover and image repos
	if coreDeps.VoiceoverRepo != nil {
		handler.SetVoiceoverRepo(coreDeps.VoiceoverRepo)
	}
	if coreDeps.ImageRepo != nil {
		handler.SetImagesRepo(coreDeps.ImageRepo)
	}
	if coreDeps.RealtimeService != nil {
		handler.SetRealtimeService(coreDeps.RealtimeService)
	}
	if coreDeps.ClipIndexerService != nil {
		handler.SetClipIndexer(coreDeps.ClipIndexerService)
	}
	if coreDeps.VectorStore != nil {
		handler.SetVectorStore(coreDeps.VectorStore)
	}
	// Create a minimal metaWriter for semantic enrichment on clip creation
	metaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)
	handler.SetMetaWriter(metaWriter)
	if coreDeps.ArtifactService != nil {
		handler.SetArtifactService(coreDeps.ArtifactService)
	}
	if coreDeps.Assets != nil {
		handler.SetAssetRepo(coreDeps.Assets.Repository())
	}
	mod := sourcespkg.NewSourcesModule(cfg, log, handler)
	log.Info("created unified Assets module")

	return &AssetsWiring{
		Handler:     handler,
		Module:      mod,
		DeletionSvc: deletionSvc,
	}, nil
}
// DriveWiring holds the Drive module wiring
type DriveWiring struct {
	Handler   *driveapi.DriveHandler
	Module    module.Module
	Reconcile *drivecleanup.Service
}

// WireDrive creates the Drive handler and module
func WireDrive(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*DriveWiring, error) {
	// Create drive uploader
	var driveUploader *drive.Uploader
	if coreDeps.DriveClient != nil {
		driveUploader = &drive.Uploader{Service: coreDeps.DriveClient, Log: log}
	}

	// Create drive reconcile service
	var reconcileSvc *drivecleanup.Service
	if driveUploader != nil {
		reconcileSvc = drivecleanup.NewService()
		log.Info("drive reconcile service initialized")
	}

	handler := driveapi.NewDriveHandler(reconcileSvc, driveUploader)
	mod := driveapi.NewModule(cfg, log, handler)
	log.Info("created Drive module")

	return &DriveWiring{
		Handler:   handler,
		Module:    mod,
		Reconcile: reconcileSvc,
	}, nil
}
type DriveDestinations struct {
	MediaRoot        string
	VideoAIRoot      string
	SoundEffectsRoot string
	imagesFolder     string
	videoAIFolder    string
}

func (d *DriveDestinations) RootFolder() string {
	return d.MediaRoot
}

func (d *DriveDestinations) ImagesFolder() string {
	return d.imagesFolder
}

func (d *DriveDestinations) VideoAIFolder() string {
	return d.videoAIFolder
}

func resolveRuntimeDestinations(ctx context.Context, db *sql.DB, driveClient *gdrive.Service, cfg *config.Config, log *zap.Logger) *DriveDestinations {
	return &DriveDestinations{
		MediaRoot:        cfg.Drive.RootFolder(),
		VideoAIRoot:      cfg.Drive.VideoAIRootFolder,
		SoundEffectsRoot: cfg.Drive.SoundEffectsRootFolder,
		imagesFolder:     cfg.Drive.ImagesFolder(),
		videoAIFolder:    cfg.Drive.VideoAIFolder(),
	}
}

func configOnlyDestinations(cfg *config.Config) *DriveDestinations {
	return &DriveDestinations{
		MediaRoot:        cfg.Drive.RootFolder(),
		VideoAIRoot:      cfg.Drive.VideoAIRootFolder,
		SoundEffectsRoot: cfg.Drive.SoundEffectsRootFolder,
		imagesFolder:     cfg.Drive.ImagesFolder(),
		videoAIFolder:    cfg.Drive.VideoAIFolder(),
	}
}
// FullImagesWiring holds the FullImages module wiring.
type FullImagesWiring struct {
	Handler *fullimageshandler.FullImagesHandler
	Module  module.Module
}

// WireFullImages creates the FullImages handler and module.
func WireFullImages(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*FullImagesWiring, error) {
	if coreDeps.ImageService == nil {
		log.Warn("fullimages: ImageService not available, skipping module")
		return nil, nil
	}

	svc := fullimages.NewService(
		coreDeps.ImageService,
		ffmpeg.NewFromConfig(cfg),
		coreDeps.MediaStore,
		cfg.Storage.ImagesPath(),
		log,
	)
	handler := fullimageshandler.NewFullImagesHandler(svc)

	mod := module.NewRouteModule(
		"fullimages",
		func(cfg *config.Config) bool { return cfg.Features.ImagesEnabled },
		"/fullimages",
		handler,
		log,
	)
	log.Info("created FullImages module using RouteModule")

	return &FullImagesWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
// ImagesWiring holds the Images module wiring
type ImagesWiring struct {
	Handler *imagesapi.ImagesHandler
	Module  module.Module
}

// WireImages creates the Images handler and module
func WireImages(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ImagesWiring, error) {
	handler := imagesapi.NewImagesHandler(coreDeps.ImageService)

	mod := imagesapi.NewModule(cfg, log, handler)
	log.Info("created Images module using api/images")

	return &ImagesWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
// JobsWiring holds the Jobs module wiring
type JobsWiring struct {
	Handler *jobsapi.JobsHandler
	Module  module.Module
}

// WireJobs creates the Jobs handler and module
func WireJobs(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*JobsWiring, error) {
	handler := jobsapi.NewJobsHandler(coreDeps.JobsService, log)

	mod := jobsapi.NewModule(cfg, log, handler)
	log.Info("created Jobs module using api/jobs")

	return &JobsWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
type MediaIngestWiring struct {
	Handler *mediaingest.MediaingestHandler
	Module  api.Module
	Service *ingest.Service
}

func WireMediaIngest(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*MediaIngestWiring, error) {
	if coreDeps == nil || coreDeps.DriveClient == nil {
		return nil, nil
	}
	if coreDeps.ImageRepo == nil || coreDeps.VoiceoverRepo == nil || coreDeps.ClipsRepo == nil || coreDeps.AssetIndexService == nil {
		return nil, nil
	}

	imagesRegistry := imgreg.NewRegistryAdapter(coreDeps.ImageRepo, cfg.Storage.ImagesPath(), log)
	imagesLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    imagesRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
		Store:       ingest.NewImageStoreAdapter(coreDeps.ImageRepo, cfg.Storage.ImagesPath()),
	}, log)

	voiceoverRegistry := voingsvc.NewVoiceoverRegistryAdapter(coreDeps.VoiceoverRepo)
	voiceoverLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    voiceoverRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
		Store:       ingest.NewVoiceoverStoreAdapter(coreDeps.VoiceoverRepo),
	}, log)

	clipRegistry := artifacts.NewClipsRegistry(
		coreDeps.DB.DB,
		coreDeps.Assets.Repository(),
		coreDeps.Assets,
		coreDeps.Assets.LocationRepository(),
		coreDeps.Assets.ProcessingRepository(),
	)
	clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
		Store: ingest.NewClipStoreAdapter(
			coreDeps.DB.DB,
			coreDeps.Assets.Repository(),
			coreDeps.Assets,
			coreDeps.Assets.LocationRepository(),
			coreDeps.Assets.ProcessingRepository(),
		),
	}, log)

	stockRegistry := artifacts.NewClipsRegistry(
		coreDeps.DB.DB,
		coreDeps.Assets.Repository(),
		coreDeps.Assets,
		coreDeps.Assets.LocationRepository(),
		coreDeps.Assets.ProcessingRepository(),
	)
	stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    stockRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
		Store: ingest.NewClipStoreAdapter(
			coreDeps.DB.DB,
			coreDeps.Assets.Repository(),
			coreDeps.Assets,
			coreDeps.Assets.LocationRepository(),
			coreDeps.Assets.ProcessingRepository(),
		),
	}, log)

	svc := ingest.NewService(cfg, log, coreDeps.DriveClient, map[ingest.Kind]*ingest.Pipeline{
		ingest.KindImage: {
			Kind:          ingest.KindImage,
			DefaultSource: "image",
			RootFolderID:  cfg.Drive.ImagesFolder(),
			RootFolder: func(req *ingest.Request) string {
				if isAIImageIngestSource(req) {
					if root := cfg.Drive.VideoAIFolder(); root != "" {
						return root
					}
				}
				return cfg.Drive.ImagesFolder()
			},
			Lifecycle: imagesLifecycle,
		},
		ingest.KindVoiceover: {
			Kind:          ingest.KindVoiceover,
			DefaultSource: "voiceover",
			RootFolderID:  cfg.Drive.VoiceoverFolder(),
			Lifecycle:     voiceoverLifecycle,
		},
		ingest.KindClip: {
			Kind:          ingest.KindClip,
			DefaultSource: "youtube",
			RootFolderID:  cfg.Drive.ClipsFolder(),
			Lifecycle:     clipLifecycle,
		},
		ingest.KindStock: {
			Kind:          ingest.KindStock,
			DefaultSource: "stock",
			RootFolderID:  cfg.Drive.StockFolder(),
			Lifecycle:     stockLifecycle,
		},
	})

	handler := mediaingest.NewMediaingestHandler(svc)
	mod := mediaingest.NewMediaIngestModule(cfg, log, handler)

	return &MediaIngestWiring{
		Handler: handler,
		Module:  mod,
		Service: svc,
	}, nil
}

func isAIImageIngestSource(req *ingest.Request) bool {
	if req == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(req.Source)) {
	case "google-vids", "google-vids-image", "google-slides", "google-flow", "nvidia", "nvidia-local", "local-nim", "flux-1-dev", "flux-1-schnell", "flux.1-schnell", "flux1-schnell", "flux-2-klein", "flux.2-klein-4b", "flux-2-klein-4b":
		return true
	default:
		return false
	}
}
// initMediaProcessor initializes the media processing engine.
func initMediaProcessor(
	cfg *config.Config,
	db *sql.DB,
	assetsRepo assets.Repository,
	querySvc *assets.Service,
	locations assets.LocationRepository,
	processing assets.ProcessingRepository,
	log *zap.Logger,
	driveUploader *drive.Uploader,
) processor.Processor {
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	httpDL := downloader.NewHTTPDownloader(5 * time.Minute)
	ffmpegProc := ffmpeg.NewFromConfig(cfg)
	clipsRegistry := artifacts.NewClipsRegistry(db, assetsRepo, querySvc, locations, processing)

	return mediaasset.NewProcessor(
		ytDLPDownloader,
		httpDL,
		ffmpegProc,
		log,
		mediaasset.ProcessorConfig{
			DataDir:            cfg.Storage.DataDir,
			TempDir:            cfg.Storage.TempDir,
			VideoCfg:           ffmpeg.DefaultNormalizeOptions(cfg),
			ScraperServerURL:   cfg.External.ArtlistScraperServerURL,
			EmbeddingServerURL: cfg.ClipIndexer.ServerURL,
		},
		clipsRegistry,
		driveUploader,
	)
}
type StockPipelineWiring struct {
	Handler *sources.StockHandler
	Module  module.Module
	Service *stockpipeline.Service
}

func WireStockPipeline(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*StockPipelineWiring, error) {
	if coreDeps.DriveClient == nil {
		log.Warn("stock pipeline not wired: missing drive client")
		return nil, nil
	}

	svc := stockpipeline.NewService(cfg, log, coreDeps.DriveClient)
	svc.SetJobsSvc(coreDeps.JobsService)
	svc.SetAssetIndex(coreDeps.AssetIndexService)
	if coreDeps.ClipsRepo != nil {
		svc.SetClipsRepo(coreDeps.ClipsRepo)
	}
	if coreDeps.YoutubeClipService != nil {
		svc.SetYoutubeService(coreDeps.YoutubeClipService)
	}
	if coreDeps.ClipIndexerService != nil {
		svc.SetClipIndexer(coreDeps.ClipIndexerService)
	}

	// Wire unified metadata writer for semantic enrichment of stock chunks
	metaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)
	svc.SetMetadataWriter(metaWriter)
	log.Info("metadata writer wired into stock pipeline")

	handler := sources.NewStockHandler(svc, coreDeps.JobsService, log)

	mod := sources.NewStockPipelineModule(cfg, log, handler)

	svc.RegisterHandler(coreDeps.JobsService)

	return &StockPipelineWiring{
		Handler: handler,
		Module:  mod,
		Service: svc,
	}, nil
}
// ensureStyleDriveFolders pre-creates the common style folders under a Drive root.
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
// buildSyncTargets creates the catalog sync targets from configuration.
// This centralizes the sync target definitions in one place.
func buildSyncTargets(
	cfg *config.Config,
	clipsOnlyRepo *clips.Repository,
	clipsRepo *clips.Repository,
	artlistRepo *clips.Repository,
) []catalogsync.Target {
	targets := []catalogsync.Target{
		{
			Name:         "stock",
			RootFolderID: cfg.Drive.StockFolder(),
			Source:       "stock",
			MediaType:    "stock",
			Repo:         clipsRepo,
		},
		{
			Name:         "youtube",
			RootFolderID: cfg.Drive.ClipsFolder(),
			Source:       "youtube",
			MediaType:    "clip",
			Repo:         clipsOnlyRepo,
		},
		{
			Name:         "artlist",
			RootFolderID: cfg.Drive.ArtlistFolder(),
			Source:       "artlist",
			MediaType:    "artlist",
			Repo:         artlistRepo,
		},
	}

	// VideoAI: sync style subfolders so other components can resolve
	// style names to Drive folder IDs via AssetTree.
	if videoAIRoot := cfg.Drive.VideoAIFolder(); videoAIRoot != "" {
		targets = append(targets, catalogsync.Target{
			Name:         "videoai",
			RootFolderID: videoAIRoot,
			Source:       "videoai",
			MediaType:    "image",
			Repo:         artlistRepo, // reuse a repo for folder metadata only
		})
	}

	return targets
}
// YouTubeClipWiring holds the YouTube Clip module wiring.
// Handler uses the new youtube subpackage type (PR-A Phase 2).
type YouTubeClipWiring struct {
	Handler *ytsources.YouTubeClipHandler
	Module  module.Module
	Service *youtube.Service
}

// WireYouTubeClip creates the YouTube Clip handler and module
func WireYouTubeClip(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*YouTubeClipWiring, error) {
	handler := ytsources.NewYouTubeClipHandler(coreDeps.YoutubeClipService, log, coreDeps.JobsService)

	var mod module.Module
	if coreDeps.YoutubeClipService != nil {
		mod = sources.NewClipsModule(cfg, log, coreDeps.YoutubeClipService, handler, coreDeps.JobsService)
		log.Info("created Clips module")

		// Register job handler for youtube_clip.extract jobs
		coreDeps.YoutubeClipService.RegisterHandler(coreDeps.JobsService)
	}

	// Wire clips repo for advanced search
	if coreDeps.ClipsRepo != nil {
		handler.SetClipsRepo(coreDeps.ClipsRepo)
	}

	return &YouTubeClipWiring{
		Handler: handler,
		Module:  mod,
		Service: coreDeps.YoutubeClipService,
	}, nil
}
