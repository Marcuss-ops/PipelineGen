package bootstrap

import (
	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"velox/go-master/internal/api/handlers/script/handlers"
	assetshandler "velox/go-master/internal/api/handlers/assets"
	artlistPkg "velox/go-master/internal/service/artlist"
	assetindex "velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/module"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// RegistryWiring holds the registry and all wired modules
type RegistryWiring struct {
	Registry    *module.Registry
	System      *SystemWiring
	ArtlistSvc  *ArtlistWiring
	YouTubeClip *YouTubeClipWiring
	Jobs       *JobsWiring
	Media      *MediaWiring
	ScriptDocs *ScriptDocsWiring
	Voiceover  *VoiceoverWiring
	Images     *ImagesWiring
	Drive      *DriveWiring
	Workflow   *WorkflowWiring
	Scraper    *ScraperWiring
	ContentPkg *ContentPackageWiring
	Assets     *AssetsWiring
}

// SystemWiring holds the System module wiring
type SystemWiring struct {
	Module module.Module
}

// ScraperWiring holds the Scraper module wiring
type ScraperWiring struct {
	Handler *scraperhandler.Handler
	Module  module.Module
}

// AssetsWiring holds the Assets module wiring
type AssetsWiring struct {
	Handler *assetshandler.Handler
	Module  module.Module
}

// WireAssets creates the Assets handler and module
func WireAssets(
	cfg *config.Config,
	log *zap.Logger,
	artlistSvc *artlistPkg.Service,
	catalogRepo *catalog.Repository,
	assetIndexSvc *assetindex.Service,
) (*AssetsWiring, error) {
	handler := assetshandler.NewHandler(artlistSvc, catalogRepo, assetIndexSvc, log)
	mod := module.NewAssetsModule(cfg, log, artlistSvc, catalogRepo, assetIndexSvc)
	log.Info("created Assets module")

	return &AssetsWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}

// WireScraper creates the Scraper handler and module
func WireScraper(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ScraperWiring, error) {
	handler := scraperhandler.NewHandler(cfg.Paths.NodeScraperDir)
	mod := module.NewScraperModule(log, handler)
	log.Info("created Scraper module")

	return &ScraperWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}

// WireSystem creates the System handler and module
func WireSystem(
	cfg *config.Config,
	log *zap.Logger,
) *SystemWiring {
	mod := module.NewSystemModule(cfg, log)
	log.Info("created System module")

	return &SystemWiring{
		Module: mod,
	}
}

// ModuleFactory defines a factory function for wiring a module
type ModuleFactory struct {
	Name   string
	Wire   func(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (module.Module, interface{}, error)
}

// wireAndRegister is a helper that wires a module and registers it if successful
func wireAndRegister(
	factory ModuleFactory,
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
	registry *module.Registry,
) interface{} {
	mod, wiring, err := factory.Wire(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", factory.Name), zap.Error(err))
		return nil
	}
	if mod != nil {
		registry.Register(mod)
		log.Info("registered module", zap.String("module", factory.Name))
	}
	return wiring
}

// WireRegistry creates and populates the module registry with all modules using factory pattern
func WireRegistry(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*RegistryWiring, error) {
	registry := module.NewRegistry()
	log.Info("module registry created")

	wiring := &RegistryWiring{
		Registry: registry,
	}

	// Define all module factories
	factories := []ModuleFactory{
		{
			Name: "System",
			Wire: func(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (module.Module, interface{}, error) {
				w := WireSystem(cfg, log)
				if w == nil {
					return nil, nil, nil
				}
				return w.Module, w, nil
			},
		},
		{
			Name: "Artlist",
			Wire: func(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (module.Module, interface{}, error) {
				w, err := WireArtlist(cfg, log, coreDeps)
				if err != nil || w == nil {
					return nil, nil, err
				}
				return w.Module, w, nil
			},
		},
		{
			Name: "ScriptDocs",
			Wire: func(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (module.Module, interface{}, error) {
				w, err := WireScriptDocs(cfg, log, coreDeps)
				if err != nil || w == nil {
					return nil, nil, err
				}
				return w.Module, w, nil
			},
		},
		{
			Name: "Voiceover",
			Wire: func(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (module.Module, interface{}, error) {
				w, err := WireVoiceover(cfg, log, coreDeps)
				if err != nil || w == nil {
					return nil, nil, err
				}
				return w.Module, w, nil
			},
		},
		{
			Name: "YouTubeClip",
			Wire: func(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (module.Module, interface{}, error) {
				w, err := WireYouTubeClip(cfg, log, coreDeps)
				if err != nil || w == nil {
					return nil, nil, err
				}
				return w.Module, w, nil
			},
		},
		{
			Name: "Jobs",
			Wire: func(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (module.Module, interface{}, error) {
				w, err := WireJobs(cfg, log, coreDeps)
				if err != nil || w == nil {
					return nil, nil, err
				}
				return w.Module, w, nil
			},
		},
		{
			Name: "Media",
			Wire: func(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (module.Module, interface{}, error) {
				w, err := WireMedia(cfg, log, coreDeps)
				if err != nil || w == nil {
					return nil, nil, err
				}
				return w.Module, w, nil
			},
		},
		{
			Name: "Images",
			Wire: func(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (module.Module, interface{}, error) {
				w, err := WireImages(cfg, log, coreDeps)
				if err != nil || w == nil {
					return nil, nil, err
				}
				return w.Module, w, nil
			},
		},
		{
			Name: "Drive",
			Wire: func(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (module.Module, interface{}, error) {
				w, err := WireDrive(cfg, log, coreDeps)
				if err != nil || w == nil {
					return nil, nil, err
				}
				return w.Module, w, nil
			},
		},
		{
			Name: "Workflow",
			Wire: func(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (module.Module, interface{}, error) {
				w, err := WireWorkflow(cfg, log, coreDeps)
				if err != nil || w == nil {
					return nil, nil, err
				}
				return w.Module, w, nil
			},
		},
		{
			Name: "Scraper",
			Wire: func(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (module.Module, interface{}, error) {
				w, err := WireScraper(cfg, log, coreDeps)
				if err != nil || w == nil {
					return nil, nil, err
				}
				return w.Module, w, nil
			},
		},
	}

	// Wire all modules using the factory pattern
	for _, factory := range factories {
		switch factory.Name {
		case "System":
			w := wireAndRegister(factory, cfg, log, coreDeps, registry)
			if w != nil {
				wiring.System = w.(*SystemWiring)
			}
		case "Artlist":
			w := wireAndRegister(factory, cfg, log, coreDeps, registry)
			if w != nil {
				wiring.ArtlistSvc = w.(*ArtlistWiring)
			}
		case "ScriptDocs":
			w := wireAndRegister(factory, cfg, log, coreDeps, registry)
			if w != nil {
				wiring.ScriptDocs = w.(*ScriptDocsWiring)
			}
		case "Voiceover":
			w := wireAndRegister(factory, cfg, log, coreDeps, registry)
			if w != nil {
				wiring.Voiceover = w.(*VoiceoverWiring)
			}
		case "YouTubeClip":
			w := wireAndRegister(factory, cfg, log, coreDeps, registry)
			if w != nil {
				wiring.YouTubeClip = w.(*YouTubeClipWiring)
			}
		case "Jobs":
			w := wireAndRegister(factory, cfg, log, coreDeps, registry)
			if w != nil {
				wiring.Jobs = w.(*JobsWiring)
			}
		case "Media":
			w := wireAndRegister(factory, cfg, log, coreDeps, registry)
			if w != nil {
				wiring.Media = w.(*MediaWiring)
			}
		case "Images":
			w := wireAndRegister(factory, cfg, log, coreDeps, registry)
			if w != nil {
				wiring.Images = w.(*ImagesWiring)
			}
		case "Drive":
			w := wireAndRegister(factory, cfg, log, coreDeps, registry)
			if w != nil {
				wiring.Drive = w.(*DriveWiring)
			}
		case "Workflow":
			w := wireAndRegister(factory, cfg, log, coreDeps, registry)
			if w != nil {
				wiring.Workflow = w.(*WorkflowWiring)
			}
		case "Scraper":
			w := wireAndRegister(factory, cfg, log, coreDeps, registry)
			if w != nil {
				wiring.Scraper = w.(*ScraperWiring)
			}
		}
	}

	// Wire ContentPackage (job handler for content.package jobs)
	contentPkgWiring, err := WireContentPackage(log, coreDeps)
	if err != nil {
		log.Warn("failed to wire ContentPackage", zap.Error(err))
	}
	if contentPkgWiring != nil {
		wiring.ContentPkg = contentPkgWiring
		log.Info("wired ContentPackage service")
	}

	// Register ScriptHistory module if available
	if coreDeps.ScriptsRepo != nil {
		scriptHistoryHandler := handlers.NewScriptHistoryHandler(coreDeps.ScriptsRepo, log)
		scriptHistoryModule := module.NewScriptHistoryModule(cfg, log, scriptHistoryHandler)
		registry.Register(scriptHistoryModule)
		log.Info("registered ScriptHistory module")
	}

	// Register Utility module
	utilityModule := module.NewUtilityModule(cfg, log, coreDeps.Utility)
	registry.Register(utilityModule)
	log.Info("registered Utility module")

	// Wire and register Assets module (unified asset search)
	assetsWiring, err := WireAssets(cfg, log, wiring.ArtlistSvc.Service, coreDeps.CatalogRepo, coreDeps.AssetIndexService)
	if err != nil {
		log.Warn("failed to wire Assets", zap.Error(err))
	}
	if assetsWiring != nil && assetsWiring.Module != nil {
		wiring.Assets = assetsWiring
		registry.Register(assetsWiring.Module)
		log.Info("registered Assets module")
	}

	return wiring, nil
}
