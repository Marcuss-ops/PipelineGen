package bootstrap

import (
	"go.uber.org/zap"
	"velox/go-master/internal/api"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/api/handlers/script"
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

	scriptDocsHandler := script.NewScriptDocsHandler(
		coreDeps.ScriptGen,
		coreDeps.DocClient,
		cfg.Storage.DataDir,
		cfg.Paths.ClipTextDir,
		cfg.Paths.PythonScriptsDir,
		cfg.Paths.NodeScraperDir,
		coreDeps.ScriptsRepo,
		cfg.Drive.StockRootFolder,
	)

	handlers := &api.Handlers{
		Health:     common.NewHealthHandler(),
		ScriptDocs: scriptDocsHandler,
		Utility:    coreDeps.Utility,
	}
	if coreDeps.ScriptsRepo != nil {
		handlers.ScriptHistory = script.NewScriptHistoryHandler(coreDeps.ScriptsRepo, log)
	}
	cleanup := func() {
		if coreClean != nil {
			coreClean()
		}
	}

	return &AppDeps{
		Handlers: handlers,
		Cleanup:  cleanup,
	}, nil
}

// WireMinimal is kept for compatibility with local tools.
func WireMinimal(cfg *config.Config, log *zap.Logger) (*AppDeps, error) {
	return WireScriptDocs(cfg, log)
}
