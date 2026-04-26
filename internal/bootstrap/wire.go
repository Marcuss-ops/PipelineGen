package bootstrap

import (
	"go.uber.org/zap"
	"velox/go-master/internal/api"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/handlers/voiceover"
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
		coreDeps.ClipsRepo,
		cfg.Drive.StockRootFolder,
	)

	handlers_struct := &api.Handlers{
		Health:     common.NewHealthHandler(),
		ScriptDocs: scriptDocsHandler,
		Voiceover:  voiceover.NewHandler(coreDeps.VoiceoverService),
		Utility:    coreDeps.Utility,
	}
	if coreDeps.ScriptsRepo != nil {
		handlers_struct.ScriptHistory = handlers.NewScriptHistoryHandler(coreDeps.ScriptsRepo, log)
	}
	cleanup := func() {
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
