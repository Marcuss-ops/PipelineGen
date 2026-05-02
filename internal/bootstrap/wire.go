package bootstrap

import (
	"path/filepath"

	"velox/go-master/internal/api"
	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/api/handlers/jobs"
	imghandler "velox/go-master/internal/api/handlers/images"
	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/handlers/voiceover"
	youtubecliphandler "velox/go-master/internal/api/handlers/youtubeclip"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/pkg/media/ffmpeg"
	drive "velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"

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
		log,
	)
	if err != nil {
		log.Warn("Failed to create Artlist service", zap.Error(err))
		artlistService = nil
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
			cfg.Paths.NodeScraperDir,
			log,
		)
	}

	// Update Voiceover service with drive destination
	if coreDeps.VoiceoverService != nil {
		coreDeps.VoiceoverService.SetDriveDestination(driveDestinationService)
	}

	// Create FFmpeg processor
	ffmpegProc := ffmpeg.New(cfg)

	// Create YouTube clip service and handler
	youtubeClipService := youtubeclip.NewService(
		cfg,
		log,
		coreDeps.ClipsOnlyRepo,
		coreDeps.DriveClient,
		driveDestinationService,
		ffmpegProc,
	)
	youtubeClipHandler := youtubecliphandler.NewHandler(youtubeClipService, log)

	jobsHandler := jobs.NewHandler(coreDeps.JobsService, log)

	handlers_struct := &api.Handlers{
		Health:      common.NewHealthHandler(),
		Artlist:     artlistHandlerVar,
		Scraper:     scraperhandler.NewHandler(cfg.Paths.NodeScraperDir),
		ImageAssets: imghandler.NewHandler(coreDeps.ImageService),
		ScriptDocs:  scriptDocsHandler,
		Voiceover:   voiceover.NewHandler(coreDeps.VoiceoverService),
		Utility:     coreDeps.Utility,
		Catalog:     common.NewCatalogHandler(coreDeps.CatalogRepo),
		YouTubeClip: youtubeClipHandler,
		Jobs:        jobsHandler,
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
