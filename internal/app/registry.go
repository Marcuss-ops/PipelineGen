package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	channelsapi "github.com/Marcuss-ops/PipelineGen/internal/api/channels"
	contentapi "github.com/Marcuss-ops/PipelineGen/internal/api/content"
	realtimeapi "github.com/Marcuss-ops/PipelineGen/internal/api/realtime"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	sourcesapi "github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	artlistadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	stockadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock"
	youtubeadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipresolver"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/media/mediaasset"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	artlistpkg "github.com/Marcuss-ops/PipelineGen/internal/application/artlist"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/upload/drive"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
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

	// ProviderRegistry is the canonical Provider registry (Agent 3
	// contract cleanup). It is constructed and frozen at the end
	// of WireRegistry so it is ready before the job runner starts.
	// Adapters register here via RegisterSearch / RegisterFetch —
	// downstream consumers (Stock PR, channel-monitor YouTube fetch)
	// resolve providers from this registry rather than wiring
	// through the legacy modules directly.
	ProviderRegistry *providers.Registry
}

func registerModule(registry *module.Registry, log *zap.Logger, mod module.Module) {
	if err := registry.Register(mod); err != nil {
		log.Warn("failed to register module", zap.String("module", mod.Name()), zap.Error(err))
	}
}

// WireRegistry creates and populates the module registry with all modules.
func WireRegistry(ctx context.Context, cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*RegistryWiring, error) {
	registry := module.NewRegistry()
	wiring := &RegistryWiring{Registry: registry}

	// ── System ─────────────────────────────────────────────────────────
	sw := WireSystem(cfg, log)
	registerModule(registry, log, sw.Module)
	wiring.System = sw

	// ── Artlist ────────────────────────────────────────────────────────
	if aw, err := WireArtlist(ctx, cfg, log, coreDeps); err != nil {
		log.Warn("failed to wire module", zap.String("module", "Artlist"), zap.Error(err))
	} else {
		registerModule(registry, log, aw.Module)
		wiring.ArtlistSvc = aw
	}

	// ── ScriptFlow ─────────────────────────────────────────────────────
	if coreDeps.ScriptGen != nil && coreDeps.ImageService != nil {
		memoryRepo := gemmamemory.NewRepository(coreDeps.DB.DB)
		memorySvc := gemmamemory.NewService(memoryRepo, log)
		scriptsRepoAdapter := scriptcore.NewRepositoryAdapter(coreDeps.ScriptsRepo)
		engine := scriptcore.NewEngine(coreDeps.ScriptGen, memorySvc, scriptsRepoAdapter, log)
		handler := scriptapi.NewScriptFlowHandler(coreDeps.ScriptGen, engine, coreDeps.ImageService, coreDeps.RealtimeService, coreDeps.AssocService, coreDeps.VoiceoverService, coreDeps.AssetTreeService, coreDeps.DocClient, coreDeps.DriveUploader, coreDeps.JobServiceFacade, scriptsRepoAdapter, memorySvc, cfg.Drive.ScriptsGenFolder(), cfg, log)
		batchSvc := scripts.NewBatchService(cfg, log, coreDeps.ScriptGen, engine, coreDeps.DocClient, coreDeps.VoiceoverService, scriptsRepoAdapter)
		handler.SetBatchService(batchSvc)
		curationSvc := scripts.NewCurationService(nil, coreDeps.JobsService, log)
		handler.SetCurationService(curationSvc)
		wireScriptFlowExtras(handler, coreDeps.ScriptGen.GetClient(), coreDeps.VectorStore, coreDeps.ClipsRepo, engine, cfg, log)
		if coreDeps.JobsService != nil {
			presetsConfig, _ := artlistpkg.LoadPresets("config/presets.yaml")
			harvestSvc := clipresolver.NewJobHarvestService(coreDeps.JobServiceFacade, log, presetsConfig, cfg.Drive.ArtlistFolder())
			handler.SetHarvestService(harvestSvc)
		}
		genSvc := scripts.NewGenerationService(coreDeps.JobServiceFacade, cfg, log)
		mod := scriptapi.NewModule(cfg, log, scriptapi.NewHandler(handler, genSvc))
		registerModule(registry, log, mod)
	}

	// ── YouTubeClip ────────────────────────────────────────────────────
	if yw, err := WireYouTubeClip(cfg, log, coreDeps); err != nil {
		log.Warn("failed to wire module", zap.String("module", "YouTubeClip"), zap.Error(err))
	} else {
		registerModule(registry, log, yw.Module)
		wiring.YouTubeClip = yw
	}

	// ── Jobs, Images, MediaIngest, Drive, Scraper, FullImages, StockPipeline ─
	for _, m := range []struct {
		name string
		fn   func() (module.Module, error)
	}{
		{"Jobs", func() (module.Module, error) {
			w, e := WireJobs(cfg, log, coreDeps)
			wiring.Jobs = w
			if w != nil {
				return w.Module, e
			}
			return nil, e
		}},
		{"Images", func() (module.Module, error) {
			w, e := WireImages(cfg, log, coreDeps)
			wiring.Images = w
			if w != nil {
				return w.Module, e
			}
			return nil, e
		}},
		{"MediaIngest", func() (module.Module, error) {
			w, e := WireMediaIngest(cfg, log, coreDeps)
			wiring.MediaIngest = w
			if w != nil {
				return w.Module, e
			}
			return nil, e
		}},
		{"Drive", func() (module.Module, error) {
			w, e := WireDrive(cfg, log, coreDeps)
			wiring.Drive = w
			if w != nil {
				return w.Module, e
			}
			return nil, e
		}},
		{"Scraper", func() (module.Module, error) {
			w, e := WireScraper(cfg, log)
			wiring.Scraper = w
			if w != nil {
				return w.Module, e
			}
			return nil, e
		}},
		{"FullImages", func() (module.Module, error) {
			w, e := WireFullImages(cfg, log, coreDeps)
			wiring.FullImages = w
			if w != nil {
				return w.Module, e
			}
			return nil, e
		}},
		{"StockPipeline", func() (module.Module, error) {
			w, e := WireStockPipeline(cfg, log, coreDeps)
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

	// ── Realtime, Books, Lessons, Channels, SearchQueries ──────────────
	if coreDeps.RealtimeService != nil {
		registerModule(registry, log, sourcesapi.NewRealtimeModule(cfg, log, realtimeapi.NewMatchHandler(coreDeps.RealtimeService, log)))
	}
	if coreDeps.BooksService != nil {
		registerModule(registry, log, contentapi.NewBooksModule(cfg, log, contentapi.NewBooksHandler(coreDeps.BooksService, coreDeps.JobServiceFacade, log)))
	}
	if coreDeps.LessonsService != nil {
		registerModule(registry, log, contentapi.NewLessonsModule(cfg, log, contentapi.NewLessonsHandler(coreDeps.LessonsService, coreDeps.JobServiceFacade, log)))
	}
	if coreDeps.DB != nil && coreDeps.DB.DB != nil {
		registerModule(registry, log, channelsapi.NewModule(log, assets.NewChannelsRepository(coreDeps.DB.DB)))
		registerModule(registry, log, sourcesapi.NewSearchQueriesModule(log, assets.NewSearchQueriesRepository(coreDeps.DB.DB)))
	}

	// ── Post-wiring cross-injections ───────────────────────────────────
	if wiring.Images != nil && wiring.MediaIngest != nil {
		if wiring.Images.Handler != nil {
			wiring.Images.Handler.SetIngestService(wiring.MediaIngest.Service)
		}
		if coreDeps.ImageService != nil {
			coreDeps.ImageService.SetIngestService(wiring.MediaIngest.Service)
		}
		log.Info("injected MediaIngest service into ImagesHandler and ImagesService")
	}
	if coreDeps.ScriptsRepo != nil {
		registerModule(registry, log, scriptapi.NewScriptHistoryModule(cfg, log, scriptapi.NewScriptHistoryHandler(scriptcore.NewRepositoryAdapter(coreDeps.ScriptsRepo), log)))
	}
	registerModule(registry, log, module.NewUtilityModule(cfg, log, coreDeps.Utility))

	// ── Maintenance Service ────────────────────────────────────────────
	maintenanceSvc := maintenance.NewService(cfg, log, coreDeps.AssetIndexService, coreDeps.AssetTreeService, coreDeps.DeletionService, coreDeps.JobsService, coreDeps.DB.DB)
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		log.Warn("failed to register maintenance handler", zap.Error(err))
	}
	coreDeps.MaintenanceService = maintenanceSvc

	// ── Assets ─────────────────────────────────────────────────────────
	var voiceoverService *voiceover.Service
	if coreDeps.VoiceoverService != nil {
		voiceoverService = coreDeps.VoiceoverService
	}
	if aw, err := WireAssets(cfg, log, coreDeps, voiceoverService, coreDeps.VoiceoverSync, coreDeps.JobsService, coreDeps.CatalogRepo, coreDeps.AssetIndexService, maintenanceSvc); err == nil && aw != nil {
		wiring.Assets = aw
		registerModule(registry, log, aw.Module)
		coreDeps.DeletionService = aw.DeletionSvc
		if maintenanceSvc != nil && aw.DeletionSvc != nil {
			maintenanceSvc.SetDeletionService(aw.DeletionSvc)
			log.Info("injected DeletionService into MaintenanceService")
		}
	}

	// ── Provider Registry (Agent 3 — providers.SearchProvider wiring) ─
	// Build the canonical providers.Registry and register every
	// available source adapter here so downstream consumers
	// (Stock PR, semantic-search callers, channel-monitor fetch
	// integration) can resolve providers from one frozen catalog
	// instead of bypassing the registry via legacy packages.
	//
	// Each registration is best-effort: missing services just skip
	// (the source wasn't wired), and a registration error is logged
	// but does NOT fail composition (operators get a warning, the
	// app still boots).
	providerReg := providers.NewRegistry()
	if wiring.ArtlistSvc != nil && wiring.ArtlistSvc.Service != nil {
		if err := providerReg.RegisterSearch(artlistadapter.NewAdapter(wiring.ArtlistSvc.Service)); err != nil {
			log.Warn("failed to register artlist provider", zap.Error(err))
		} else {
			log.Info("registered artlist provider in providers.Registry")
		}
	} else {
		log.Info("artlist service unavailable — skipping provider registration")
	}
	if wiring.YouTubeClip != nil && wiring.YouTubeClip.Service != nil {
		if err := providerReg.RegisterSearch(youtubeadapter.NewAdapter(wiring.YouTubeClip.Service)); err != nil {
			log.Warn("failed to register youtube provider", zap.Error(err))
		} else {
			log.Info("registered youtube provider in providers.Registry")
		}
	} else {
		log.Info("youtube clip service unavailable — skipping provider registration")
	}
	// ── Stock FetchProvider (Wave 12 turn 2: FIRST real FetchProvider) ──
	// Stock is the first adapter in the codebase to satisfy providers.FetchProvider
	// (artlist and youtube implement SearchProvider only — see adapter.go
	// preambles). The Stock service is composed earlier in the loop, so
	// wiring.StockPipeline.Service is already available at this point.
	if wiring.StockPipeline != nil && wiring.StockPipeline.Service != nil {
		if err := providerReg.RegisterFetch(stockadapter.NewAdapter(wiring.StockPipeline.Service)); err != nil {
			log.Warn("failed to register stock fetch provider", zap.Error(err))
		} else {
			log.Info("registered stock fetch provider in providers.Registry")
		}
	} else {
		log.Info("stock pipeline service unavailable — skipping fetch provider registration")
	}
	// Freeze so lookups become effectively wait-free and no further
	// Register calls succeed. Mirrors the freeze timing of the
	// module registry (see bootstrap.go's WireServices).
	providerReg.Freeze()
	wiring.ProviderRegistry = providerReg
	// Also expose via CoreDeps so cross-cutting code that doesn't
	// have access to the wiring struct (eg. background jobs that
	// only see CoreDeps) can resolve providers from the same frozen
	// catalog.
	coreDeps.ProviderRegistry = providerReg
	// Wire registry into the already-constructed SourcesHandler.
	// Search handlers (handler_sources_search_handlers.go) dispatch
	// via ByCapability(CapabilitySearch) — this is the canonical
	// path. Late-binding matches the existing Set* setter pattern
	// (SetRealtimeService, SetClipIndexer, etc.).
	if wiring.Assets != nil && wiring.Assets.Handler != nil {
		wiring.Assets.Handler.SetProviderRegistry(providerReg)
		log.Info("wired providers.Registry into SourcesHandler for Search dispatch")
	}
	// Wire registry into the YouTubeClip handler. This advances
	// docs/migration-maps/internal-sources.md cut-over step 1:
	// "finish provider-registry migration" by reducing the legacy
	// direct callers of internal/sources/youtube.Service from the
	// HTTP transport layer. The handler falls back to the direct
	// call if SetProviderRegistry fails to resolve a "youtube"
	// SearchProvider, so a missing provider never regresses the
	// endpoint.
	if wiring.YouTubeClip != nil && wiring.YouTubeClip.Handler != nil {
		wiring.YouTubeClip.Handler.SetProviderRegistry(providerReg)
		log.Info("wired providers.Registry into YouTubeClipHandler for Search dispatch")
	}
	log.Info("providers.Registry wired and frozen",
		zap.Int("providers", len(providerReg.All())))

	return wiring, nil
}

// wireScriptFlowExtras wires optional clip-source builder and media curator.
func wireScriptFlowExtras(handler *scriptapi.ScriptFlowHandler, ollamaClient *client.Client, vectorStore *vectorstore.Service, clipsOnlyRepo *assets.ClipsRepository, engine *scriptcore.Engine, cfg *config.Config, log *zap.Logger) {
	if ollamaClient == nil {
		return
	}
	clipSourceBuilder := scriptcore.NewClipSourceBuilder(clipsOnlyRepo, ollamaClient, log)
	if vectorStore != nil && cfg.Features.CatalogScriptVectorSearch {
		clipSourceBuilder.SetVectorStore(vectorStore)
	}
	if cfg.Reranker.Enabled {
		rerankerCli := reranker.NewClient(reranker.Config{Enabled: cfg.Reranker.Enabled, URL: cfg.Reranker.URL, Model: cfg.Reranker.Model, TopK: cfg.Reranker.TopK, TimeoutMs: cfg.Reranker.TimeoutMs})
		clipSourceBuilder.SetReranker(rerankerCli)
	}
	handler.SetClipSourceBuilder(clipSourceBuilder)
	handler.SetCurationClipSourceBuilder(clipSourceBuilder)
	if (vectorStore != nil || clipsOnlyRepo != nil) && engine != nil {
		handler.SetMediaCurator(scripts.NewMediaCurator(vectorStore, cfg.ClipIndexer.ServerURL, clipsOnlyRepo, clipSourceBuilder, engine, log))
	}
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
	return mediaasset.NewProcessor(ytDLPDownloader, httpDL, ffmpegProc, log, mediaasset.ProcessorConfig{DataDir: cfg.Storage.DataDir, TempDir: cfg.Storage.TempDir, VideoCfg: ffmpeg.DefaultNormalizeOptions(cfg), ScraperServerURL: cfg.External.ArtlistScraperServerURL, EmbeddingServerURL: cfg.ClipIndexer.ServerURL}, clipsRegistry, driveUploader)
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
