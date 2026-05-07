package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/harvester"
	"velox/go-master/internal/repository/images"
	jobrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/assetregistry"
	"velox/go-master/internal/service/association"
	"velox/go-master/internal/service/catalogsync"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/indexing"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/mediaasset"
	"velox/go-master/internal/service/mediaregistry"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/service/voiceoversync"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/media/ffmpeg"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
)

type services struct {
	scriptGen          *ollama.Generator
	docClient          drive.DocClient
	driveClient        *gdrive.Service
	utility            *common.UtilityHandler
	scriptsRepo        *scripts.ScriptRepository
	imageRepo          *images.Repository
	imageService       *imgservice.Service
	stockDriveRepo     *clips.Repository
	artlistRepo        *clips.Repository
	clipsOnlyRepo      *clips.Repository
	monitorsRepo       *monitors.Repository
	voiceoverService   *voiceover.Service
	voiceoverSync      *voiceoversync.Service
	indexingService    *indexing.Service
	harvesterRepo      *harvester.Repository
	catalogRepo        *catalog.Repository
	catalogSync        *catalogsync.Service
	assocService       *association.Service
	jobsRepo           *jobrepo.Repository
	jobsService        *jobservice.Service
	jobsDispatcher     *jobservice.Dispatcher
	mediaProcessor     processor.Processor
	youtubeClipService *youtubeclip.Service
	assetIndexService  *assetindex.Service
	assetResolver      *assetindex.Resolver
	assetRegistry      *assetregistry.Registry
}

func initServices(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger) (*services, error) {
	ollamaClient := client.NewClient(cfg.External.OllamaURL, cfg.External.OllamaModel, cfg.External.OllamaTimeoutSeconds)
	scriptGen := ollama.NewGenerator(ollamaClient)

	docClient, err := drive.NewDocClient(ctx, cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		log.Warn("Docs client not initialized", zap.Error(err))
	}

	driveClient, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		log.Warn("Google Drive client not initialized", zap.Error(err))
	}

	// Create media processor with both yt-dlp and HTTP downloaders
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	httpDL := downloader.NewHTTPDownloader(5 * time.Minute)
	ffmpegProc := ffmpeg.New(cfg)
	clipsOnlyRepo := clips.NewRepository(dbs.clips.DB, log)
	clipsRegistry := mediaregistry.NewClipsRegistry(clipsOnlyRepo)
	mediaProcessorInternal := mediaasset.NewProcessor(
		ytDLPDownloader,
		httpDL,
		ffmpegProc,
		log,
		mediaasset.ProcessorConfig{
			DataDir:  cfg.Storage.DataDir,
			TempDir:  cfg.Storage.TempDir,
			VideoCfg: ffmpeg.DefaultNormalizeOptions(cfg),
		},
		clipsRegistry,
	)
	mediaProcessor := mediaasset.ToCoreProcessor(mediaProcessorInternal)

	// Asset index service
	assetIndexRepo := assetindex.NewRepository(dbs.assets.DB)
	assetIndexService := assetindex.NewService(assetIndexRepo)
	log.Info("asset index service initialized", zap.String("db", "assets.db.sqlite"))

	monitorsRepo := monitors.NewRepository(dbs.main.DB)

	// Create LifecycleService for youtubeclip using common factory
	ytLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipsRegistry,
		DriveClient: driveClient,
		AssetIndex:  assetIndexService,
	}, log)

	youtubeClipService := youtubeclip.NewService(
		cfg,
		log,
		clipsOnlyRepo,
		monitorsRepo,
		driveClient,
		mediaProcessor,
		ytLifecycle,
	)

	voDir := filepath.Join(cfg.Storage.DataDir, cfg.Storage.VoiceoversDir)
	if err := os.MkdirAll(voDir, 0755); err != nil {
		log.Warn("Failed to create voiceovers directory", zap.Error(err))
	}
	voRepo := voiceovers.NewRepository(dbs.voiceover.DB)

	// Create voiceover registry adapter
	voRegistryAdapter := voiceover.NewVoiceoverRegistryAdapter(voRepo)

	// Create LifecycleService for voiceover using common factory
	voLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    voRegistryAdapter,
		DriveClient: driveClient,
		AssetIndex:  assetIndexService,
	}, log)

	voService := voiceover.NewService(cfg, cfg.Paths.PythonScriptsDir, voDir, log, driveClient, voLifecycle)
	log.Info("Voiceover service initialized", zap.String("python_scripts_dir", cfg.Paths.PythonScriptsDir))

	clipsRepo := clips.NewRepository(dbs.stock.DB, log)
	artlistRepo := clips.NewRepository(dbs.artlist.DB, log)

	// Create and initialize central asset registry
	assetRegistry := assetregistry.NewRegistry(log)
	assetRegistry.RegisterClipSource(assetregistry.AssetSourceStock, assetregistry.NewClipsAdapter(clipsRepo, log))
	assetRegistry.RegisterClipSource(assetregistry.AssetSourceYouTube, assetregistry.NewClipsAdapter(clipsOnlyRepo, log))
	assetRegistry.RegisterClipSource(assetregistry.AssetSourceArtlist, assetregistry.NewClipsAdapter(artlistRepo, log))
	log.Info("central asset registry initialized with clip sources")

	scriptsRepo := scripts.NewScriptRepository(dbs.main.DB)
	imageRepo := images.NewRepository(dbs.images.DB)

	imgAssetsDir := filepath.Join(cfg.Storage.DataDir, cfg.Storage.AssetsDir)
	if err := os.MkdirAll(imgAssetsDir, 0755); err != nil {
		log.Warn("Failed to create image assets directory", zap.Error(err))
	}
	imageService := imgservice.NewService(imageRepo, imgAssetsDir, assetIndexService, log)

	// Asset resolver (queries asset_index first, then falls back to specific DBs)
	clipsRepos := map[string]*clips.Repository{
		"youtube": clipsOnlyRepo,
		"stock":    clipsRepo,
		"artlist":  artlistRepo,
	}
	resolverCfg := &assetindex.ResolverConfig{
		ClipsRepos:    clipsRepos,
		ImageRepo:     imageRepo,
		VoiceoverRepo: voRepo,
	}
	assetResolver := assetindex.NewResolver(assetIndexService, resolverCfg, log)
	log.Info("asset resolver initialized")

	harvesterRepo := harvester.NewRepository(dbs.main.DB, log)
	indexingService := indexing.NewService(clipsRepo, log)
	catalogRepo := catalog.NewRepository(clipsOnlyRepo, clipsRepo, artlistRepo)

	assocService := association.NewService(cfg.Storage.DataDir, cfg.Paths.NodeScraperDir, clipsRepo, artlistRepo, clipsOnlyRepo, catalogRepo)

	// Build sync targets centrally
	syncTargets := buildSyncTargets(cfg, clipsOnlyRepo, clipsRepo, artlistRepo)

	catalogSync := catalogsync.NewService(driveClient, syncTargets, assetIndexService, log)

	// Voiceover sync service
	var voiceoverSync *voiceoversync.Service
	if cfg.Drive.VoiceoverRootFolder != "" && voRepo != nil {
		voiceoverSync = voiceoversync.NewService(driveClient, voRepo, cfg.Drive.VoiceoverRootFolder, log)
		log.Info("Voiceover sync service initialized", zap.String("root_folder_id", cfg.Drive.VoiceoverRootFolder))
	}

	// Jobs system
	jobsRepo := jobrepo.NewRepository(dbs.jobs.DB, log)
	jobsDispatcher := jobservice.NewDispatcher()
	if os.Getenv("VELOX_ENABLE_TEST_JOB_HANDLERS") == "true" {
		jobservice.RegisterTestHandlers(jobsDispatcher, log)
	}
	jobsService := jobservice.NewService(jobsRepo, jobsDispatcher, log)

		return &services{
		scriptGen:          scriptGen,
		docClient:          docClient,
		driveClient:        driveClient,
		utility:            common.NewUtilityHandler(),
		scriptsRepo:        scriptsRepo,
		imageRepo:          imageRepo,
		imageService:       imageService,
		stockDriveRepo:     clipsRepo,
		artlistRepo:        artlistRepo,
		clipsOnlyRepo:      clipsOnlyRepo,
		monitorsRepo:       monitorsRepo,
		voiceoverService:   voService,
		voiceoverSync:      voiceoverSync,
		indexingService:    indexingService,
		harvesterRepo:      harvesterRepo,
		catalogRepo:        catalogRepo,
		catalogSync:        catalogSync,
		assocService:       assocService,
		jobsRepo:           jobsRepo,
		jobsService:        jobsService,
		jobsDispatcher:     jobsDispatcher,
		mediaProcessor:     mediaProcessor,
		youtubeClipService: youtubeClipService,
		assetIndexService:  assetIndexService,
		assetResolver:      assetResolver,
		assetRegistry:      assetRegistry,
	}, nil
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
			RootFolderID: cfg.Drive.StockRootFolder,
			Source:       "stock",
			MediaType:    "stock",
			Repo:         clipsRepo,
		},
		{
			Name:         "clips",
			RootFolderID: cfg.Drive.ClipsRootFolder,
			Source:       "clips",
			MediaType:    "clip",
			Repo:         clipsOnlyRepo,
		},
		{
			Name:         "artlist",
			RootFolderID: cfg.Harvester.DriveFolderID,
			Source:       "artlist",
			MediaType:    "artlist",
			Repo:         artlistRepo,
		},
	}

	if cfg.Drive.ClipRootFolders != nil {
		for group, folderID := range cfg.Drive.ClipRootFolders {
			if folderID != "" {
				targets = append(targets, catalogsync.Target{
					Name:         "clips_" + group,
					RootFolderID: folderID,
					Source:       "clips",
					MediaType:    "clip",
					Repo:         clipsOnlyRepo,
				})
			}
		}
	}

	return targets
}
