package app

import (
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/config"
	"velox/go-master/internal/core/maintenance"
	"velox/go-master/internal/media/voiceover"
	"velox/go-master/internal/module"
	"velox/go-master/internal/sources/artlist"
	"velox/go-master/internal/sources/youtube"

	"go.uber.org/zap"
)

// RegistryWiring holds the registry and all wired modules
type RegistryWiring struct {
	Registry         *module.Registry
	System           *SystemWiring
	ArtlistSvc       *ArtlistWiring
	YouTubeClip      *YouTubeClipWiring
	Jobs             *JobsWiring
	ScriptDocs       *ScriptDocsWiring
	Voiceover        *VoiceoverWiring
	Images           *ImagesWiring
	Drive            *DriveWiring
	Scraper          *ScraperWiring
	ContentPkg       *ContentPackageWiring
	Assets           *AssetsWiring
	StockPipeline    *StockPipelineWiring
	GoogleAccounting *GoogleAccountingWiring
}

// WireRegistry creates and populates the module registry with all modules using factory pattern
func WireRegistry(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*RegistryWiring, error) {
	registry := module.NewRegistry()
	wiring := &RegistryWiring{Registry: registry}

	// Module registration configuration
	type reg struct {
		name   string
		wire   func() (module.Module, interface{}, error)
		assign func(interface{})
	}

	modules := []reg{
		{"System", func() (module.Module, interface{}, error) {
			w := WireSystem(cfg, log)
			return w.Module, w, nil
		}, func(w interface{}) { wiring.System = w.(*SystemWiring) }},
		{"GoogleAccounting", func() (module.Module, interface{}, error) {
			w, err := WireGoogleAccounting(cfg, log)
			if err != nil {
				return nil, nil, err
			}
			return w.Module, w, nil
		}, func(w interface{}) { wiring.GoogleAccounting = w.(*GoogleAccountingWiring) }},
		{"Artlist", func() (module.Module, interface{}, error) {
			w, err := WireArtlist(cfg, log, coreDeps)
			if err != nil {
				return nil, nil, err
			}
			return w.Module, w, nil
		}, func(w interface{}) { wiring.ArtlistSvc = w.(*ArtlistWiring) }},
		{"ScriptDocs", func() (module.Module, interface{}, error) {
			w, err := WireScriptDocs(cfg, log, coreDeps)
			if err != nil {
				return nil, nil, err
			}
			return w.Module, w, nil
		}, func(w interface{}) { wiring.ScriptDocs = w.(*ScriptDocsWiring) }},
		{"Voiceover", func() (module.Module, interface{}, error) {
			w, err := WireVoiceover(cfg, log, coreDeps)
			if err != nil {
				return nil, nil, err
			}
			return w.Module, w, nil
		}, func(w interface{}) { wiring.Voiceover = w.(*VoiceoverWiring) }},
		{"YouTubeClip", func() (module.Module, interface{}, error) {
			w, err := WireYouTubeClip(cfg, log, coreDeps)
			if err != nil {
				return nil, nil, err
			}
			return w.Module, w, nil
		}, func(w interface{}) { wiring.YouTubeClip = w.(*YouTubeClipWiring) }},
		{"Jobs", func() (module.Module, interface{}, error) {
			w, err := WireJobs(cfg, log, coreDeps)
			if err != nil {
				return nil, nil, err
			}
			return w.Module, w, nil
		}, func(w interface{}) { wiring.Jobs = w.(*JobsWiring) }},
		{"Images", func() (module.Module, interface{}, error) {
			w, err := WireImages(cfg, log, coreDeps)
			if err != nil {
				return nil, nil, err
			}
			return w.Module, w, nil
		}, func(w interface{}) { wiring.Images = w.(*ImagesWiring) }},
		{"Drive", func() (module.Module, interface{}, error) {
			w, err := WireDrive(cfg, log, coreDeps)
			if err != nil {
				return nil, nil, err
			}
			return w.Module, w, nil
		}, func(w interface{}) { wiring.Drive = w.(*DriveWiring) }},
		{"Scraper", func() (module.Module, interface{}, error) {
			w, err := WireScraper(cfg, log, coreDeps)
			if err != nil {
				return nil, nil, err
			}
			return w.Module, w, nil
		}, func(w interface{}) { wiring.Scraper = w.(*ScraperWiring) }},
		{"StockPipeline", func() (module.Module, interface{}, error) {
			w, err := WireStockPipeline(cfg, log, coreDeps)
			if err != nil {
				return nil, nil, err
			}
			if w == nil {
				return nil, nil, nil
			}
			return w.Module, w, nil
		}, func(w interface{}) { wiring.StockPipeline = w.(*StockPipelineWiring) }},
	}

	for _, m := range modules {
		mod, w, err := m.wire()
		if err != nil {
			log.Warn("failed to wire module", zap.String("module", m.name), zap.Error(err))
			continue
		}
		if mod != nil {
			registry.Register(mod)
			if m.assign != nil && w != nil {
				m.assign(w)
			}
			log.Info("registered module", zap.String("module", m.name))
		}
	}

	// Post-wiring injection
	if wiring.ScriptDocs != nil && wiring.ArtlistSvc != nil && wiring.ScriptDocs.Handler != nil {
		wiring.ScriptDocs.Handler.SetArtlistService(wiring.ArtlistSvc.Service)
		log.Info("injected ArtlistService into ScriptDocsHandler")
	}

	// Remaining specific wiring
	if contentPkgWiring, err := WireContentPackage(log, coreDeps); err == nil && contentPkgWiring != nil {
		wiring.ContentPkg = contentPkgWiring
	}

	if coreDeps.ScriptsRepo != nil {
		registry.Register(module.NewScriptHistoryModule(cfg, log, handlers.NewScriptHistoryHandler(coreDeps.ScriptsRepo, log)))
	}

	registry.Register(module.NewUtilityModule(cfg, log, coreDeps.Utility))

	// Maintenance service (must be initialized before assets for registration)
	maintenanceSvc := maintenance.NewService(cfg, log, coreDeps.AssetIndexService, coreDeps.AssetTreeService, coreDeps.DeletionService, coreDeps.JobsService, coreDeps.DB.DB)
	maintenanceSvc.RegisterHandler()
	coreDeps.MaintenanceService = maintenanceSvc

	var artlistService *artlist.Service
	if wiring.ArtlistSvc != nil {
		artlistService = wiring.ArtlistSvc.Service
	}

	var youtubeClipService *youtube.Service
	if wiring.YouTubeClip != nil {
		youtubeClipService = wiring.YouTubeClip.Service
	}

	var voiceoverService *voiceover.Service
	if wiring.Voiceover != nil {
		voiceoverService = wiring.Voiceover.Service
	}

	if assetsWiring, err := WireAssets(
		cfg,
		log,
		coreDeps,
		artlistService,
		youtubeClipService,
		voiceoverService,
		coreDeps.JobsService,
		coreDeps.CatalogRepo,
		coreDeps.AssetIndexService,
		maintenanceSvc,
	); err == nil && assetsWiring != nil {
		wiring.Assets = assetsWiring
		registry.Register(assetsWiring.Module)
		coreDeps.DeletionService = assetsWiring.DeletionSvc

		// Inject real deletion service into maintenance
		if maintenanceSvc != nil && assetsWiring.DeletionSvc != nil {
			maintenanceSvc.SetDeletionService(assetsWiring.DeletionSvc)
			log.Info("injected DeletionService into MaintenanceService")
		}
	}

	return wiring, nil
}
