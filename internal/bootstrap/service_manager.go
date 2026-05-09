package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	jobrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/repository/voiceovers"
	assettree_repo "velox/go-master/internal/repository/assettree"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/assetregistry"
	"velox/go-master/internal/service/assettree"
	"velox/go-master/internal/service/association"
	"velox/go-master/internal/service/catalogsync"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/indexing"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/mediaregistry"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/service/voiceoversync"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)


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

	// 4. Media Processing
	clipsOnlyRepo := clips.NewRepository(dbs.clips.DB, log)
	mediaProcessor := initMediaProcessor(cfg, clipsOnlyRepo, log)
	clipsRegistry := mediaregistry.NewClipsRegistry(clipsOnlyRepo)

	// Asset index service
	assetIndexRepo := assetindex.NewRepository(dbs.assets.DB)
	assetIndexService := assetindex.NewService(assetIndexRepo)
	log.Info("asset index service initialized", zap.String("db", "assets.db.sqlite"))

	// Asset tree service
	assetTreeRepo, err := assettree_repo.NewRepository(dbs.assets.DB, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize asset tree repository: %w", err)
	}
	assetTreeService := assettree.NewService(assetTreeRepo, log)
	log.Info("asset tree service initialized")

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
	imageService := imgservice.NewService(imageRepo, imgAssetsDir, assetIndexService, driveClient, cfg.Drive.ImagesRootFolder, cfg, log)

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

	indexingService := indexing.NewService(clipsRepo, log)
	catalogRepo := catalog.NewRepository(clipsOnlyRepo, clipsRepo, artlistRepo)

	assocService := association.NewService(cfg.Storage.DataDir, cfg.Paths.NodeScraperDir, clipsRepo, artlistRepo, clipsOnlyRepo, catalogRepo)

	// Build sync targets centrally
	syncTargets := buildSyncTargets(cfg, clipsOnlyRepo, clipsRepo, artlistRepo)

	catalogSync := catalogsync.NewService(driveClient, syncTargets, assetIndexService, assetTreeService, log)

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
		catalogRepo:        catalogRepo,
		catalogSync:        catalogSync,
		assocService:       assocService,
		jobsRepo:           jobsRepo,
		jobsService:        jobsService,
		jobsDispatcher:     jobsDispatcher,
		mediaProcessor:     mediaProcessor,
		youtubeClipService: youtubeClipService,
		assetIndexService:  assetIndexService,
		assetTreeService:   assetTreeService,
		assetResolver:      assetResolver,
		assetRegistry:      assetRegistry,
	}, nil
}

