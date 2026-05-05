package bootstrap

import (
	"velox/go-master/internal/api"
	scriptHandlers "velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/module"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// AppDeps holds the minimal initialized dependencies for the server.
type AppDeps struct {
	Handlers *api.Handlers
	Registry  *module.Registry
	Cleanup  func()
}

// WireServices initializes the full server composition root.
func WireServices(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	coreDeps, coreClean, err := initCoreMinimal(cfg, log, mode)
	if err != nil {
		return nil, err
	}

	// Wire up the registry with all modules
	registryWiring, err := WireRegistry(cfg, log, coreDeps)
	if err != nil {
		return nil, err
	}

	// Build the handlers struct
	handlers := &api.Handlers{
		Health:        common.NewHealthHandler(),
		Artlist:       registryWiring.ArtlistSvc.Handler,
		Scraper:       registryWiring.Scraper.Handler,
		ImageAssets:   registryWiring.Images.Handler,
		Media:         registryWiring.Media.Handler,
		ScriptDocs:    registryWiring.ScriptDocs.Handler,
		Voiceover:     registryWiring.Voiceover.Handler,
		VoiceoverSync: registryWiring.Voiceover.SyncHandler,
		Utility:       coreDeps.Utility,
		Catalog:       common.NewCatalogHandler(coreDeps.CatalogRepo),
		YouTubeClip:   registryWiring.YouTubeClip.Handler,
		Jobs:          registryWiring.Jobs.Handler,
		Drive:         registryWiring.Drive.Handler,
		Workflow:      registryWiring.Workflow.Handler,
	}

	// Add ScriptHistory if available
	if coreDeps.ScriptsRepo != nil {
		scriptHistoryHandler := scriptHandlers.NewScriptHistoryHandler(coreDeps.ScriptsRepo, log)
		handlers.ScriptHistory = scriptHistoryHandler
	}

	cleanup := func() {
		if registryWiring.ArtlistSvc != nil && registryWiring.ArtlistSvc.Service != nil {
			registryWiring.ArtlistSvc.Service.Close()
		}
		if coreClean != nil {
			coreClean()
		}
	}

	return &AppDeps{
		Handlers: handlers,
		Registry:  registryWiring.Registry,
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
