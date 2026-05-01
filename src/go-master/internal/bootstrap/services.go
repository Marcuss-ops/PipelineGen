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
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/service/catalogsync"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/indexing"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
)

type services struct {
	scriptGen        *ollama.Generator
	docClient        drive.DocClient
	driveClient      *gdrive.Service
	utility          *common.UtilityHandler
	scriptsRepo      *scripts.ScriptRepository
	imageRepo        *images.Repository
	imageService     *imgservice.Service
	stockDriveRepo   *clips.Repository
	artlistRepo      *clips.Repository
	clipsOnlyRepo    *clips.Repository
	voiceoverService *voiceover.Service
	indexingService  *indexing.Service
	harvesterRepo    *harvester.Repository
	catalogRepo      *catalog.Repository
	catalogSync      *catalogsync.Service
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

	voDir := filepath.Join(cfg.Storage.DataDir, cfg.Storage.VoiceoversDir)
	if err := os.MkdirAll(voDir, 0755); err != nil {
		log.Warn("Failed to create voiceovers directory", zap.Error(err))
	}
	voService := voiceover.NewService(cfg.Paths.PythonScriptsDir, voDir, log)

	clipsRepo := clips.NewRepository(dbs.stock.DB)
	artlistRepo := clips.NewRepository(dbs.artlist.DB)
	clipsOnlyRepo := clips.NewRepository(dbs.clips.DB)

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

	catalogSync := catalogsync.NewService(driveClient, []catalogsync.Target{
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
	}, log)

	return &services{
		scriptGen:        scriptGen,
		docClient:        docClient,
		driveClient:      driveClient,
		utility:          common.NewUtilityHandler(),
		scriptsRepo:      scriptsRepo,
		imageRepo:        imageRepo,
		imageService:     imageService,
		stockDriveRepo:   clipsRepo,
		artlistRepo:      artlistRepo,
		clipsOnlyRepo:    clipsOnlyRepo,
		voiceoverService: voService,
		indexingService:  indexingService,
		harvesterRepo:    harvesterRepo,
		catalogRepo:      catalogRepo,
		catalogSync:      catalogSync,
	}, nil
}
