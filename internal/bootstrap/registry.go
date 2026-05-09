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
	Scraper    *ScraperWiring
	ContentPkg *ContentPackageWiring
	Assets     *AssetsWiring
	AssetTree  *AssetTreeWiring
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


// WireRegistry creates and populates the module registry with all modules using factory pattern

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
		{"Artlist", func() (module.Module, interface{}, error) { return WireArtlist(cfg, log, coreDeps) }, 
			func(w interface{}) { wiring.ArtlistSvc = w.(*ArtlistWiring) }},
		{"ScriptDocs", func() (module.Module, interface{}, error) { return WireScriptDocs(cfg, log, coreDeps) }, 
			func(w interface{}) { wiring.ScriptDocs = w.(*ScriptDocsWiring) }},
		{"Voiceover", func() (module.Module, interface{}, error) { return WireVoiceover(cfg, log, coreDeps) }, 
			func(w interface{}) { wiring.Voiceover = w.(*VoiceoverWiring) }},
		{"YouTubeClip", func() (module.Module, interface{}, error) { return WireYouTubeClip(cfg, log, coreDeps) }, 
			func(w interface{}) { wiring.YouTubeClip = w.(*YouTubeClipWiring) }},
		{"Jobs", func() (module.Module, interface{}, error) { return WireJobs(cfg, log, coreDeps) }, 
			func(w interface{}) { wiring.Jobs = w.(*JobsWiring) }},
		{"Media", func() (module.Module, interface{}, error) { return WireMedia(cfg, log, coreDeps) }, 
			func(w interface{}) { wiring.Media = w.(*MediaWiring) }},
		{"Images", func() (module.Module, interface{}, error) { return WireImages(cfg, log, coreDeps) }, 
			func(w interface{}) { wiring.Images = w.(*ImagesWiring) }},
		{"Drive", func() (module.Module, interface{}, error) { return WireDrive(cfg, log, coreDeps) }, 
			func(w interface{}) { wiring.Drive = w.(*DriveWiring) }},
		{"Scraper", func() (module.Module, interface{}, error) { return WireScraper(cfg, log, coreDeps) }, 
			func(w interface{}) { wiring.Scraper = w.(*ScraperWiring) }},
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

	// Remaining specific wiring
	if contentPkgWiring, err := WireContentPackage(log, coreDeps); err == nil && contentPkgWiring != nil {
		wiring.ContentPkg = contentPkgWiring
	}

	if coreDeps.ScriptsRepo != nil {
		registry.Register(module.NewScriptHistoryModule(cfg, log, handlers.NewScriptHistoryHandler(coreDeps.ScriptsRepo, log)))
	}

	registry.Register(module.NewUtilityModule(cfg, log, coreDeps.Utility))

	if assetsWiring, err := WireAssets(cfg, log, wiring.ArtlistSvc.Service, coreDeps.CatalogRepo, coreDeps.AssetIndexService); err == nil && assetsWiring != nil {
		wiring.Assets = assetsWiring
		registry.Register(assetsWiring.Module)
	}

	if assetTreeWiring, err := WireAssetTree(cfg, log, coreDeps); err == nil && assetTreeWiring != nil {
		wiring.AssetTree = assetTreeWiring
		registry.Register(assetTreeWiring.Module)
	}

	return wiring, nil
}
