package app

import (
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/module"
	scriptdocssvc "velox/go-master/internal/media/scriptdocs"
	"velox/go-master/internal/config"

	"go.uber.org/zap"
)

// ScriptDocsWiring holds the ScriptDocs module wiring
type ScriptDocsWiring struct {
	Handler      *handlers.ScriptDocsHandler
	Module       module.Module
	ScriptSvc    *scriptdocssvc.Service
	ScriptJobSvc *scriptdocssvc.JobService
}

// WireScriptDocs creates the ScriptDocs handler and module
func WireScriptDocs(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ScriptDocsWiring, error) {
	// Create scriptdocs service
	var scriptSvc *scriptdocssvc.Service
	if coreDeps.ScriptGen != nil && coreDeps.ScriptsRepo != nil {
		scriptSvc = scriptdocssvc.NewService(coreDeps.ScriptGen, coreDeps.ScriptsRepo, log)
		log.Info("created scriptdocs service")
	}

	// Create scriptjob service and register handler
	var scriptJobSvc *scriptdocssvc.JobService
	if scriptSvc != nil && coreDeps.JobsService != nil {
		scriptJobSvc = scriptdocssvc.NewJobService(log, scriptSvc, coreDeps.JobsService)
		scriptJobSvc.RegisterHandler(coreDeps.JobsService)
		log.Info("registered script.generate job handler")
	}

	// Create persistence service
	persistSvc := scriptdocssvc.NewPersistenceService(coreDeps.ScriptsRepo, coreDeps.JobsService, log)

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
		coreDeps.JobsService,
		coreDeps.ClipResolver,
		persistSvc,
	)

	var mod module.Module
	if handler != nil {
		mod = module.NewScriptDocsModule(cfg, log, handler)
		log.Info("created ScriptDocs module")
	}

	return &ScriptDocsWiring{
		Handler:      handler,
		Module:       mod,
		ScriptSvc:    scriptSvc,
		ScriptJobSvc: scriptJobSvc,
	}, nil
}
