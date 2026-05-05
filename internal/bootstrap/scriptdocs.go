package bootstrap

import (
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/module"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// ScriptDocsWiring holds the ScriptDocs module wiring
type ScriptDocsWiring struct {
	Handler *handlers.ScriptDocsHandler
	Module  module.Module
}

// WireScriptDocs creates the ScriptDocs handler and module
func WireScriptDocs(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ScriptDocsWiring, error) {
	handler := handlers.NewScriptDocsHandler(
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
		nil, // artlistSvc - will be set later if available
		coreDeps.AssocService,
	)

	var mod module.Module
	if handler != nil {
		mod = module.NewScriptDocsModule(cfg, log, handler)
		log.Info("created ScriptDocs module")
	}

	return &ScriptDocsWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
