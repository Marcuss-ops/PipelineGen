package app

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/core/destination"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/books"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/media/images"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/images"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/voiceovers"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
)

// initVoiceoverService sets up the voiceover service and its repository.
func initVoiceoverService(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger,
	driveClient *gdrive.Service, driveUploader *drive.Uploader,
	assetIndexService *assetindex.Service, clipIndexerService *clipindexer.Service,
	destResolver destination.Resolver) (*voiceover.Service, *voiceovers.Repository) {

	voDir := cfg.Storage.VoiceoversPath()
	voRepo := voiceovers.NewRepository(dbs.main.DB)

	// Create voiceover registry adapter
	voRegistryAdapter := voiceover.NewVoiceoverRegistryAdapter(voRepo)

	// Create LifecycleService for voiceover using common factory
	voLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    voRegistryAdapter,
		DriveClient: driveClient,
		AssetIndex:  assetIndexService,
	}, log)

	voService := voiceover.NewService(cfg, dbs.main.DB, cfg.Paths.PythonScriptsDir, voDir, log, driveUploader, voLifecycle, destResolver)
	log.Info("Voiceover service initialized", zap.String("python_scripts_dir", cfg.Paths.PythonScriptsDir))

	// Wire clip indexer for voiceover embedding + Qdrant upsert
	if clipIndexerService.IsEnabled() {
		voService.SetClipIndexer(func(ctx context.Context, assetID string) error {
			return clipIndexerService.IndexClip(ctx, assetID)
		})
		log.Info("clip indexer wired into voiceover service for semantic search")
	}

	// Wire Ollama translator for promo voiceover generation
	return voService, voRepo
}

// initBooksService creates the books processing service.
func initBooksService(cfg *config.Config, dbs *databases, log *zap.Logger, driveUploader *drive.Uploader, voiceoverSvc *voiceover.Service) *books.Service {
	booksSvc := books.NewService(&books.Config{
		Enabled:       cfg.Books.Enabled,
		ScriptPath:    cfg.Books.ScriptPath,
		PythonBin:     cfg.Books.PythonBin,
		DriveFolderID: cfg.Drive.BooksFolder(),
	}, dbs.main.DB, cfg.Drive.BooksFolder(), log, voiceoverSvc)
	if driveUploader != nil {
		booksSvc.SetDriveUploader(driveUploader)
	}
	log.Info("Books service initialized", zap.Bool("enabled", cfg.Books.Enabled))
	return booksSvc
}

// initImageService creates the image generation service and metadata writer.
func initImageService(ctx context.Context, cfg *config.Config, log *zap.Logger,
	driveClient *gdrive.Service, clipsRepo *clips.Repository, artlistRepo *clips.Repository,
	styleRegistry *generation.StyleRegistry, scriptGen *ollama.Generator,
	mediaStore *storage.Store, vectorSvc *vectorstore.Service,
	imageRepo *images.Repository) (*imgservice.Service, *semantic.MetadataWriter) {

	imageService := imgservice.NewService(cfg, imageRepo, clipsRepo, driveClient, styleRegistry, log)
	imageService.SetNvidiaConfig(cfg.External.NvidiaAPIKey, cfg.External.NvidiaModel)
	imageService.SetGoogleAccountingConfig(
		cfg.GoogleAccounting.ServerURL,
		cfg.GoogleAccounting.DownloadDir,
		cfg.GoogleAccounting.VidsProjectID,
	)

	// Wire remote image endpoint (Google Flow on external server)
	if cfg.External.RemoteImageEndpointURL != "" {
		imageService.SetRemoteImageEndpointURL(cfg.External.RemoteImageEndpointURL)
		log.Info("Remote image endpoint configured", zap.String("url", cfg.External.RemoteImageEndpointURL))
	}

	// Wire Velox base URL for push-mode webhook delivery
	if cfg.External.VeloxBaseURL != "" {
		imageService.SetVeloxBaseURL(cfg.External.VeloxBaseURL)
		log.Info("Velox base URL for webhook push configured", zap.String("url", cfg.External.VeloxBaseURL))
	}

	imageService.SetMediaStore(mediaStore)
	imageService.SetLLMGenerator(scriptGen)
	if vectorSvc != nil {
		imageService.SetVectorStore(vectorSvc)
	}

	// Wire unified metadata writer into image service
	metaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)
	imageService.SetMetadataWriter(metaWriter)

	return imageService, metaWriter
}
