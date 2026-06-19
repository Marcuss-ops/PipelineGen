package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	booksapi "github.com/Marcuss-ops/PipelineGen/internal/api/books"
	channelsapi "github.com/Marcuss-ops/PipelineGen/internal/api/channels"
	lessonsapi "github.com/Marcuss-ops/PipelineGen/internal/api/lessons"
	realtimeapi "github.com/Marcuss-ops/PipelineGen/internal/api/realtime"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	sourcesapi "github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/batch"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/curation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/generate"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
// duplicate curation import removed
	"github.com/Marcuss-ops/PipelineGen/internal/core/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	sqlite "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"



	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipresolver"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/media/mediaasset"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts/gemmamemory"
	artlistpkg "github.com/Marcuss-ops/PipelineGen/internal/sources/artlist"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"
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
		engine := scriptcore.NewEngine(coreDeps.ScriptGen, memorySvc, coreDeps.ScriptsRepo, log)
		handler := scriptapi.NewScriptFlowHandler(coreDeps.ScriptGen, engine, coreDeps.ImageService, coreDeps.RealtimeService, coreDeps.AssocService, coreDeps.VoiceoverService, coreDeps.AssetTreeService, coreDeps.DocClient, coreDeps.DriveUploader, coreDeps.JobsService, coreDeps.ScriptsRepo, memorySvc, cfg.Drive.ScriptsGenFolder(), cfg, log)
		batchSvc := batch.NewBatchService(cfg, log, coreDeps.ScriptGen, engine, coreDeps.DocClient, coreDeps.VoiceoverService, coreDeps.ScriptsRepo)
		handler.SetBatchService(batchSvc)
		curationSvc := curation.NewCurationService(nil, coreDeps.JobsService, log)
		handler.SetCurationService(curationSvc)
		wireScriptFlowExtras(handler, coreDeps.ScriptGen.GetClient(), coreDeps.VectorStore, coreDeps.ClipsRepo, engine, cfg, log)
		if coreDeps.JobsService != nil {
			presetsConfig, _ := artlistpkg.LoadPresets("config/presets.yaml")
			harvestSvc := clipresolver.NewJobHarvestService(coreDeps.JobsService, log, presetsConfig, cfg.Drive.ArtlistFolder())
			handler.SetHarvestService(harvestSvc)
		}
		genSvc := generate.NewGenerationService(coreDeps.JobsService, cfg, log)
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
		{"Jobs", func() (module.Module, error) { w, e := WireJobs(cfg, log, coreDeps); wiring.Jobs = w; if w != nil { return w.Module, e }; return nil, e }},
		{"Images", func() (module.Module, error) { w, e := WireImages(cfg, log, coreDeps); wiring.Images = w; if w != nil { return w.Module, e }; return nil, e }},
		{"MediaIngest", func() (module.Module, error) { w, e := WireMediaIngest(cfg, log, coreDeps); wiring.MediaIngest = w; if w != nil { return w.Module, e }; return nil, e }},
		{"Drive", func() (module.Module, error) { w, e := WireDrive(cfg, log, coreDeps); wiring.Drive = w; if w != nil { return w.Module, e }; return nil, e }},
		{"Scraper", func() (module.Module, error) { w, e := WireScraper(cfg, log); wiring.Scraper = w; if w != nil { return w.Module, e }; return nil, e }},
		{"FullImages", func() (module.Module, error) { w, e := WireFullImages(cfg, log, coreDeps); wiring.FullImages = w; if w != nil { return w.Module, e }; return nil, e }},
		{"StockPipeline", func() (module.Module, error) { w, e := WireStockPipeline(cfg, log, coreDeps); wiring.StockPipeline = w; if w != nil { return w.Module, e }; return nil, e }},
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
		registerModule(registry, log, booksapi.NewModule(cfg, log, booksapi.NewBooksHandler(coreDeps.BooksService, coreDeps.JobsService, log)))
	}
	if coreDeps.LessonsService != nil {
		registerModule(registry, log, lessonsapi.NewModule(cfg, log, lessonsapi.NewLessonsHandler(coreDeps.LessonsService, coreDeps.JobsService, log)))
	}
	if coreDeps.DB != nil && coreDeps.DB.DB != nil {
		registerModule(registry, log, channelsapi.NewModule(log, sqlite.NewChannelsRepository(coreDeps.DB.DB)))
		registerModule(registry, log, sourcesapi.NewSearchQueriesModule(log, sqlite.NewSearchQueriesRepository(coreDeps.DB.DB)))
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
		registerModule(registry, log, scriptapi.NewScriptHistoryModule(cfg, log, scriptapi.NewScriptHistoryHandler(coreDeps.ScriptsRepo, log)))
	}
	registerModule(registry, log, module.NewUtilityModule(cfg, log, coreDeps.Utility))

	// ── Maintenance Service ────────────────────────────────────────────
	maintenanceSvc := maintenance.NewService(cfg, log, coreDeps.AssetIndexService, coreDeps.AssetTreeService, coreDeps.DeletionService, coreDeps.JobsService, coreDeps.DB.DB)
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		log.Warn("failed to register maintenance handler", zap.Error(err))
	}
	coreDeps.MaintenanceService = maintenanceSvc

	// ── Assets ─────────────────────────────────────────────────────────
	var artlistService *artlistpkg.Service
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
	if aw, err := WireAssets(cfg, log, coreDeps, artlistService, youtubeClipService, voiceoverService, coreDeps.VoiceoverSync, coreDeps.JobsService, coreDeps.CatalogRepo, coreDeps.AssetIndexService, maintenanceSvc); err == nil && aw != nil {
		wiring.Assets = aw
		registerModule(registry, log, aw.Module)
		coreDeps.DeletionService = aw.DeletionSvc
		if maintenanceSvc != nil && aw.DeletionSvc != nil {
			maintenanceSvc.SetDeletionService(aw.DeletionSvc)
			log.Info("injected DeletionService into MaintenanceService")
		}
	}

	return wiring, nil
}

// wireScriptFlowExtras wires optional clip-source builder and media curator.
func wireScriptFlowExtras(handler *scriptapi.ScriptFlowHandler, ollamaClient *client.Client, vectorStore *vectorstore.Service, clipsOnlyRepo *sqlite.ClipsRepository, engine *scriptcore.Engine, cfg *config.Config, log *zap.Logger) {
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
		handler.SetMediaCurator(curation.NewMediaCurator(vectorStore, cfg.ClipIndexer.ServerURL, clipsOnlyRepo, clipSourceBuilder, engine, log))
	}
}

func initAssetServices(dbs *databases, log *zap.Logger) (*assetindex.Service, *assettree.Service, error) {
	assetIndexRepo := assetindex.NewRepository(dbs.main.DB)
	assetIndexService := assetindex.NewService(assetIndexRepo)
	assetTreeRepo, err := sqlite.NewAssetTreeRepository(dbs.main.DB, log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize asset tree repository: %w", err)
	}
	assetTreeService := assettree.NewService(assetTreeRepo, log)
	return assetIndexService, assetTreeService, nil
}

type DriveDestinations struct {
	MediaRoot, VideoAIRoot, SoundEffectsRoot, imagesFolder, videoAIFolder string
}

func (d *DriveDestinations) RootFolder() string     { return d.MediaRoot }
func (d *DriveDestinations) ImagesFolder() string   { return d.imagesFolder }
func (d *DriveDestinations) VideoAIFolder() string  { return d.videoAIFolder }

func resolveRuntimeDestinations(ctx context.Context, db *sql.DB, driveClient *gdrive.Service, cfg *config.Config, log *zap.Logger) *DriveDestinations {
	return &DriveDestinations{MediaRoot: cfg.Drive.RootFolder(), VideoAIRoot: cfg.Drive.VideoAIRootFolder, SoundEffectsRoot: cfg.Drive.SoundEffectsRootFolder, imagesFolder: cfg.Drive.ImagesFolder(), videoAIFolder: cfg.Drive.VideoAIFolder()}
}

func configOnlyDestinations(cfg *config.Config) *DriveDestinations {
	return &DriveDestinations{MediaRoot: cfg.Drive.RootFolder(), VideoAIRoot: cfg.Drive.VideoAIRootFolder, SoundEffectsRoot: cfg.Drive.SoundEffectsRootFolder, imagesFolder: cfg.Drive.ImagesFolder(), videoAIFolder: cfg.Drive.VideoAIFolder()}
}

func initMediaProcessor(cfg *config.Config, db *sql.DB, assetsRepo assets.Repository, querySvc *assets.Service, locations assets.LocationRepository, processing assets.ProcessingRepository, log *zap.Logger, driveUploader *driveup.Uploader) processor.Processor {
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	httpDL := downloader.NewHTTPDownloader(5 * time.Minute)
	ffmpegProc := ffmpeg.NewFromConfig(cfg)
	clipsRegistry := artifacts.NewClipsRegistry(db, assetsRepo, querySvc, locations, processing)
	return mediaasset.NewProcessor(ytDLPDownloader, httpDL, ffmpegProc, log, mediaasset.ProcessorConfig{DataDir: cfg.Storage.DataDir, TempDir: cfg.Storage.TempDir, VideoCfg: ffmpeg.DefaultNormalizeOptions(cfg), ScraperServerURL: cfg.External.ArtlistScraperServerURL, EmbeddingServerURL: cfg.ClipIndexer.ServerURL}, clipsRegistry, driveUploader)
}

func buildSyncTargets(cfg *config.Config, clipsOnlyRepo *sqlite.ClipsRepository, clipsRepo *sqlite.ClipsRepository, artlistRepo *sqlite.ClipsRepository) []catalogsync.Target {
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