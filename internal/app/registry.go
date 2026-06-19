package app

import (
	"context"

	handlers "github.com/Marcuss-ops/PipelineGen/internal/api"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	booksapi "github.com/Marcuss-ops/PipelineGen/internal/api/books"
	batchpkg "github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/batch"
	curationpkg "github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/curation"
	channelsapi "github.com/Marcuss-ops/PipelineGen/internal/api/channels"
	lessonsapi "github.com/Marcuss-ops/PipelineGen/internal/api/lessons"
	realtimeapi "github.com/Marcuss-ops/PipelineGen/internal/api/realtime"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	searchqueriesapi "github.com/Marcuss-ops/PipelineGen/internal/api/searchqueries"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/core/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipresolver"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
	channelsrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/channels"
	searchqueriesrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/searchqueries"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts/gemmamemory"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/generate"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"

	"go.uber.org/zap"
)

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
		handler := handlers.NewScriptFlowHandler(
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
		handler := handlers.NewMatchHandler(coreDeps.RealtimeService, log)
		thin := realtimeapi.NewHandler(handler)
		mod := realtimeapi.NewModule(cfg, log, thin)
		registerModule(registry, log, mod)
	}

	// ── Books ──────────────────────────────────────────────────────────
	if coreDeps.BooksService != nil {
		handler := booksapi.NewBooksHandler(coreDeps.BooksService, coreDeps.JobsService, log)
		thin := booksapi.NewHandler(handler)
		mod := booksapi.NewModule(cfg, log, thin)
		registerModule(registry, log, mod)
	}

	// ── Lessons ────────────────────────────────────────────────────────
	if coreDeps.LessonsService != nil {
		handler := lessonsapi.NewLessonsHandler(coreDeps.LessonsService, coreDeps.JobsService, log)
		thin := lessonsapi.NewHandler(handler)
		mod := lessonsapi.NewModule(cfg, log, thin)
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
		mod := searchqueriesapi.NewModule(log, repo)
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
		registerModule(registry, log, module.NewScriptHistoryModule(
			cfg, log, handlers.NewScriptHistoryHandler(coreDeps.ScriptsRepo, log),
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
