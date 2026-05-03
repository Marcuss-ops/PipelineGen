package bootstrap

import (
	"path/filepath"

	"velox/go-master/internal/api"
	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/api/handlers/common"
	drivehandler "velox/go-master/internal/api/handlers/drive"
	"velox/go-master/internal/api/handlers/jobs"
	imghandler "velox/go-master/internal/api/handlers/images"
	mediahandler "velox/go-master/internal/api/handlers/media"
	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/handlers/voiceover"
	youtubecliphandler "velox/go-master/internal/api/handlers/youtubeclip"
	"velox/go-master/internal/core/media"
	mediarepo "velox/go-master/internal/repository/media"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/drivecleanup"
	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/service/mediaregistry"
	drive "velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"

	"go.uber.org/zap"
)

// AppDeps holds the minimal initialized dependencies for the server.
type AppDeps struct {
	Handlers *api.Handlers
	Cleanup  func()
}

// WireServices initializes the full server composition root.
func WireServices(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	return WireScriptDocs(cfg, log, mode)
}

// WireScriptDocs initializes the minimal text->doc server.
func WireScriptDocs(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	coreDeps, coreClean, err := initCoreMinimal(cfg, log, mode)
	if err != nil {
		return nil, err
	}

	// Create drive destination service first
	driveDestinationService := drivedestination.NewService(cfg, log, coreDeps.DriveClient)

	// Create Artlist service with drive destination
	artlistDBPath := filepath.Join(cfg.Storage.DataDir, "artlist.db.sqlite")
	driveFolderID := drive.ResolveArtlistRootFolderID(cfg)
	var artlistService *artlist.Service

	// Create mediaregistry components for Artlist
	clipsRegistry := mediaregistry.NewClipsRegistry(coreDeps.ArtlistRepo)
	driveVerifier := mediaregistry.NewAPIDriveVerifier(coreDeps.DriveClient)
	mediaFinalizer := mediaregistry.NewFinalizer(clipsRegistry, driveVerifier, log)

	// Create Artlist DriveService
	artlistDriveService := artlist.NewDriveService(coreDeps.DriveClient, driveFolderID, driveDestinationService, log)

	artlistService, err = artlist.NewService(
		cfg,
		coreDeps.DB.DB,
		coreDeps.JobsDB,
		artlistDBPath,
		cfg.Paths.NodeScraperDir,
		coreDeps.ArtlistRepo,
		artlistDriveService,
		coreDeps.MediaProcessor,
		mediaFinalizer,
		log,
	)
	if err != nil {
		log.Warn("Failed to create Artlist service", zap.Error(err))
		artlistService = nil
	}

	// Create drive cleanup service (for trash/delete sync between SQLite and Drive)
	var driveCleanupSvc *drivecleanup.Service
	if coreDeps.DriveClient != nil {
		driveCleanupSvc = drivecleanup.NewService(coreDeps.ArtlistRepo, coreDeps.DriveClient, log, true)
		log.Info("drive cleanup service initialized (trash mode)")
	}

	// Register artlist job handler
	if artlistService != nil && coreDeps.JobsService != nil {
		coreDeps.JobsService.RegisterHandler(models.JobTypeArtlistRun, artlistService.HandleJob)
		log.Info("registered artlist job handler")
	}

	scriptDocsHandler := handlers.NewScriptDocsHandler(
		coreDeps.ScriptGen,
		coreDeps.DocClient,
		coreDeps.VoiceoverService,
		coreDeps.ImageService,
		cfg.Storage.DataDir,
		cfg.Paths.ClipTextDir,
		cfg.Paths.PythonScriptsDir,
		cfg.Paths.NodeScraperDir,
		coreDeps.ScriptsRepo,
		coreDeps.StockDriveRepo,
		coreDeps.ArtlistRepo,
		coreDeps.ClipsOnlyRepo,
		cfg.Drive.StockRootFolder,
		artlistService,
		coreDeps.AssocService,
	)

	// Create Artlist handler
	var artlistHandlerVar *artlistHandler.Handler
	if artlistService != nil {
		artlistHandlerVar = artlistHandler.NewHandler(
			artlistService,
			coreDeps.CatalogSyncService,
			coreDeps.JobsService,
			cfg.Paths.NodeScraperDir,
			log,
		)
	}

	// Voiceover service is already initialized with drive destination in WireMinimal
	if coreDeps.VoiceoverService != nil {
		log.Info("voiceover service initialized with unified destination resolver")
	}

	// Use YouTube clip service from core deps
	youtubeClipHandler := youtubecliphandler.NewHandler(coreDeps.YoutubeClipService, log)

	jobsHandler := jobs.NewHandler(coreDeps.JobsService, log)

	// Create media handler with ManifestExporter
	var mediaHandler *mediahandler.Handler
	mediaRepo := mediarepo.NewRepository(coreDeps.DB.DB)
	if mediaRepo != nil {
		exporter := media.NewManifestExporter(mediaRepo)
		mediaHandler = mediahandler.NewHandler(exporter)
	}

	handlers_struct := &api.Handlers{
		Health:         common.NewHealthHandler(),
		Artlist:        artlistHandlerVar,
		Scraper:        scraperhandler.NewHandler(cfg.Paths.NodeScraperDir),
		ImageAssets:    imghandler.NewHandler(coreDeps.ImageService),
		Media:          mediaHandler,
		ScriptDocs:     scriptDocsHandler,
		Voiceover:      voiceover.NewHandler(coreDeps.VoiceoverService),
		VoiceoverSync:  voiceover.NewSyncHandler(coreDeps.VoiceoverSync, log),
		Utility:        coreDeps.Utility,
		Catalog:        common.NewCatalogHandler(coreDeps.CatalogRepo),
		YouTubeClip:    youtubeClipHandler,
		Jobs:           jobsHandler,
		Drive:          drivehandler.NewHandler(driveCleanupSvc),
	}
	if coreDeps.ScriptsRepo != nil {
		handlers_struct.ScriptHistory = handlers.NewScriptHistoryHandler(coreDeps.ScriptsRepo, log)
	}
	cleanup := func() {
		if artlistService != nil {
			artlistService.Close()
		}
		if coreClean != nil {
			coreClean()
		}
	}

	return &AppDeps{
		Handlers: handlers_struct,
		Cleanup:  cleanup,
	}, nil
}

// WireMinimal creates a minimal server with core services only.
// This is the recommended entry point for local tools and minimal deployments.
func WireMinimal(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	_, coreClean, err := initCoreMinimal(cfg, log, mode)
	if err != nil {
		return nil, err
	}
	return &AppDeps{
		Handlers: nil, // Minimal mode doesn't set up handlers
		Cleanup:  coreClean,
	}, nil
}
