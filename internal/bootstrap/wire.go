package bootstrap

import (
	"path/filepath"

	"velox/go-master/internal/api"
	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/api/handlers/jobs"
	imghandler "velox/go-master/internal/api/handlers/images"
	mediahandler "velox/go-master/internal/api/handlers/media"
	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/handlers/voiceover"
	youtubecliphandler "velox/go-master/internal/api/handlers/youtubeclip"
	"velox/go-master/internal/core/media"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/service/youtubeclip"
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

// WireServices initializes the minimal server composition root.
func WireServices(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	return WireMinimal(cfg, log, mode)
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
	artlistService, err = artlist.NewService(
		cfg,
		coreDeps.DB.DB,
		artlistDBPath,
		cfg.Paths.NodeScraperDir,
		coreDeps.ArtlistRepo,
		coreDeps.DriveClient,
		driveFolderID,
		driveDestinationService,
		coreDeps.MediaProcessor,
		log,
	)
	if err != nil {
		log.Warn("Failed to create Artlist service", zap.Error(err))
		artlistService = nil
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

	// Update Voiceover service with drive destination
	if coreDeps.VoiceoverService != nil {
		coreDeps.VoiceoverService.SetDriveDestination(driveDestinationService)
	}

	// Create YouTube clip service and handler
	youtubeClipService := youtubeclip.NewService(
		cfg,
		log,
		coreDeps.ClipsOnlyRepo,
		coreDeps.MonitorsRepo,
		coreDeps.DriveClient,
		driveDestinationService,
		coreDeps.MediaProcessor,
	)
	youtubeClipHandler := youtubecliphandler.NewHandler(youtubeClipService, log)

	jobsHandler := jobs.NewHandler(coreDeps.JobsService, log)

	// Create media handler with ManifestExporter
	var mediaHandler *mediahandler.Handler
	mediaRepo := media.NewClipsRepositoryAdapter(coreDeps.ClipsOnlyRepo)
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

// WireMinimal is kept for compatibility with local tools.
func WireMinimal(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	return WireScriptDocs(cfg, log, mode)
}
