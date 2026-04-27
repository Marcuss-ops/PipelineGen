package bootstrap

import (
	"path/filepath"

	"go.uber.org/zap"
	"velox/go-master/internal/api"
	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/api/handlers/common"
	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/handlers/voiceover"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/pkg/config"
)

// AppDeps holds the minimal initialized dependencies for the server.
type AppDeps struct {
	Handlers *api.Handlers
	Cleanup  func()
}

// WireServices initializes the minimal server composition root.
func WireServices(cfg *config.Config, log *zap.Logger) (*AppDeps, error) {
	return WireMinimal(cfg, log)
}

// WireScriptDocs initializes the minimal text->doc server.
func WireScriptDocs(cfg *config.Config, log *zap.Logger) (*AppDeps, error) {
	coreDeps, coreClean, err := initCoreMinimal(cfg, log)
	if err != nil {
		return nil, err
	}

	scriptDocsHandler := handlers.NewScriptDocsHandler(
		coreDeps.ScriptGen,
		coreDeps.DocClient,
		coreDeps.VoiceoverService,
		cfg.Storage.DataDir,
		cfg.Paths.ClipTextDir,
		cfg.Paths.PythonScriptsDir,
		cfg.Paths.NodeScraperDir,
		coreDeps.ScriptsRepo,
		coreDeps.StockDriveRepo,
		coreDeps.ArtlistRepo,
		coreDeps.ClipsOnlyRepo,
		cfg.Drive.StockRootFolder,
	)

	// Create Artlist service
	artlistDBPath := filepath.Join(cfg.Paths.NodeScraperDir, "artlist_videos.db")
	artlistService, err := artlist.NewService(
		coreDeps.DB.DB,
		artlistDBPath,
		cfg.Paths.NodeScraperDir,
		coreDeps.ArtlistRepo,
		coreDeps.DocClient,
		cfg.Harvester.DriveFolderID,
		log,
	)
	if err != nil {
		log.Warn("Failed to create Artlist service", zap.Error(err))
	}

	// Create Artlist handler
	var artlistHandlerVar *artlistHandler.Handler
	if artlistService != nil {
		artlistHandlerVar = artlistHandler.NewHandler(
			artlistService,
			cfg.Paths.NodeScraperDir,
			log,
		)
	}

	handlers_struct := &api.Handlers{
		Health:     common.NewHealthHandler(),
		Artlist:    artlistHandlerVar,
		Scraper:    scraperhandler.NewHandler(cfg.Paths.NodeScraperDir),
		ScriptDocs: scriptDocsHandler,
		Voiceover:  voiceover.NewHandler(coreDeps.VoiceoverService),
		Utility:    coreDeps.Utility,
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
func WireMinimal(cfg *config.Config, log *zap.Logger) (*AppDeps, error) {
	return WireScriptDocs(cfg, log)
}
