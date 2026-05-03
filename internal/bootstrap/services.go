package bootstrap

import (
	"context"
	"os"
	"path/filepath"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/harvester"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/monitors"
	jobrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/service/association"
	"velox/go-master/internal/service/catalogsync"
	"velox/go-master/internal/service/voiceoversync"
	jobservice "velox/go-master/internal/service/jobs"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/indexing"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/service/mediaasset"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/media/ffmpeg"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
)

type services struct {
	scriptGen         *ollama.Generator
	docClient         drive.DocClient
	driveClient       *gdrive.Service
	utility           *common.UtilityHandler
	scriptsRepo       *scripts.ScriptRepository
	imageRepo         *images.Repository
	imageService      *imgservice.Service
	stockDriveRepo    *clips.Repository
	artlistRepo       *clips.Repository
	clipsOnlyRepo     *clips.Repository
	monitorsRepo      *monitors.Repository
	voiceoverService  *voiceover.Service
	voiceoverSync     *voiceoversync.Service
	indexingService   *indexing.Service
	harvesterRepo     *harvester.Repository
	catalogRepo       *catalog.Repository
	catalogSync       *catalogsync.Service
	assocService      *association.Service
	jobsRepo          *jobrepo.Repository
	jobsService       *jobservice.Service
	jobsDispatcher    *jobservice.Dispatcher
	mediaProcessor    *mediaasset.Processor
	youtubeClipService *youtubeclip.Service
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

	// Create media processor
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	ffmpegProc := ffmpeg.New(cfg)
	mediaProcessor := mediaasset.NewProcessor(
		ytDLPDownloader,
		ffmpegProc,
		driveClient,
		log,
		mediaasset.ProcessorConfig{
			DataDir: cfg.Storage.DataDir,
			TempDir: cfg.Storage.TempDir,
			VideoCfg: ffmpeg.DefaultNormalizeOptions(cfg),
		},
	)

	driveDestinationService := drivedestination.NewService(cfg, log, driveClient)

	monitorsRepo := monitors.NewRepository(dbs.main.DB)
	clipsOnlyRepo := clips.NewRepository(dbs.clips.DB, log)

	youtubeClipService := youtubeclip.NewService(
		cfg,
		log,
		clipsOnlyRepo,
		monitorsRepo,
		driveClient,
		driveDestinationService,
		mediaProcessor,
	)

	voDir := filepath.Join(cfg.Storage.DataDir, cfg.Storage.VoiceoversDir)
	if err := os.MkdirAll(voDir, 0755); err != nil {
		log.Warn("Failed to create voiceovers directory", zap.Error(err))
	}
	voRepo := voiceovers.NewRepository(dbs.voiceover.DB)
	voService := voiceover.NewService(cfg, cfg.Paths.PythonScriptsDir, voDir, log, driveClient, voRepo)
	log.Info("Voiceover service initialized", zap.String("python_scripts_dir", cfg.Paths.PythonScriptsDir))

	clipsRepo := clips.NewRepository(dbs.stock.DB, log)
	artlistRepo := clips.NewRepository(dbs.artlist.DB, log)

	if err := clipsOnlyRepo.EnsureSegmentEmbeddingsSchema(ctx); err != nil {
		log.Warn("Failed to ensure segment embeddings cache schema", zap.Error(err))
	}

	scriptsRepo := scripts.NewScriptRepository(dbs.main.DB)
	imageRepo := images.NewRepository(dbs.images.DB)

	imgAssetsDir := filepath.Join(cfg.Storage.DataDir, cfg.Storage.AssetsDir)
	if err := os.MkdirAll(imgAssetsDir, 0755); err != nil {
		log.Warn("Failed to create image assets directory", zap.Error(err))
	}
	imageService := imgservice.NewService(imageRepo, imgAssetsDir, log)

	harvesterRepo := harvester.NewRepository(dbs.main.DB, log)
	indexingService := indexing.NewService(clipsRepo, log)
	catalogRepo := catalog.NewRepository(cfg.Storage.DataDir)

	assocService := association.NewService(cfg.Storage.DataDir, cfg.Paths.NodeScraperDir, clipsRepo, artlistRepo, clipsOnlyRepo, catalogRepo)

	syncTargets := []catalogsync.Target{
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
				syncTargets = append(syncTargets, catalogsync.Target{
					Name:         "clips_" + group,
					RootFolderID: folderID,
					Source:       "clips",
					MediaType:    "clip",
					Repo:         clipsOnlyRepo,
				})
			}
		}
	}

	catalogSync := catalogsync.NewService(driveClient, syncTargets, log)

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
		scriptGen:         scriptGen,
		docClient:         docClient,
		driveClient:       driveClient,
		utility:           common.NewUtilityHandler(),
		scriptsRepo:       scriptsRepo,
		imageRepo:         imageRepo,
		imageService:      imageService,
		stockDriveRepo:    clipsRepo,
		artlistRepo:       artlistRepo,
		clipsOnlyRepo:     clipsOnlyRepo,
		monitorsRepo:      monitorsRepo,
		voiceoverService:  voService,
		voiceoverSync:     voiceoverSync,
		indexingService:   indexingService,
		harvesterRepo:     harvesterRepo,
		catalogRepo:       catalogRepo,
		catalogSync:       catalogSync,
		assocService:      assocService,
		jobsRepo:          jobsRepo,
		jobsService:       jobsService,
		jobsDispatcher:    jobsDispatcher,
		mediaProcessor:    mediaProcessor,
		youtubeClipService: youtubeClipService,
	}, nil
}
