package bootstrap

import (
	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"velox/go-master/internal/api/handlers/script/handlers"
	assetshandler "velox/go-master/internal/api/handlers/assets"
	artlistPkg "velox/go-master/internal/service/artlist"
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

// WireAssets creates the Assets handler and module
func WireAssets(
	cfg *config.Config,
	log *zap.Logger,
	artlistSvc *artlistPkg.Service,
	catalogRepo *catalog.Repository,
) (*AssetsWiring, error) {
	handler := assetshandler.NewHandler(artlistSvc, catalogRepo, log)
	mod := module.NewAssetsModule(cfg, log, artlistSvc, catalogRepo)
	log.Info("created Assets module")

	return &AssetsWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}

// AssetsWiring holds the Assets module wiring
type AssetsWiring struct {
	Handler *assetshandler.Handler
	Module  module.Module
}

// WireScraper creates the Scraper handler and module
func WireScraper(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ScraperWiring, error) {
	handler := scraperhandler.NewHandler(cfg.Paths.NodeScraperDir)
	mod := module.NewScraperModule(cfg, log, handler)
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

// WireRegistry creates and populates the module registry with all modules
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

	// Wire System (doctor endpoint)
	systemWiring := WireSystem(cfg, log)
	if systemWiring != nil && systemWiring.Module != nil {
		wiring.System = systemWiring
		registry.Register(systemWiring.Module)
		log.Info("registered System module")
	}

	// Wire Artlist
	artlistWiring, err := WireArtlist(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire Artlist", zap.Error(err))
	}
	if artlistWiring != nil && artlistWiring.Module != nil {
		wiring.ArtlistSvc = artlistWiring
		registry.Register(artlistWiring.Module)
		log.Info("registered Artlist module")
	}

	// Wire ScriptDocs
	scriptDocsWiring, err := WireScriptDocs(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire ScriptDocs", zap.Error(err))
	}
	if scriptDocsWiring != nil && scriptDocsWiring.Module != nil {
		wiring.ScriptDocs = scriptDocsWiring
		registry.Register(scriptDocsWiring.Module)
		log.Info("registered ScriptDocs module")
	}

	// Wire Voiceover
	voiceoverWiring, err := WireVoiceover(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire Voiceover", zap.Error(err))
	}
	if voiceoverWiring != nil && voiceoverWiring.Module != nil {
		wiring.Voiceover = voiceoverWiring
		registry.Register(voiceoverWiring.Module)
		log.Info("registered Voiceover module")
	}

	// Wire YouTubeClip
	youtubeClipWiring, err := WireYouTubeClip(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire YouTubeClip", zap.Error(err))
	}
	if youtubeClipWiring != nil && youtubeClipWiring.Module != nil {
		wiring.YouTubeClip = youtubeClipWiring
		registry.Register(youtubeClipWiring.Module)
		log.Info("registered YouTube Clips module")
	}

	// Wire Jobs
	jobsWiring, err := WireJobs(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire Jobs", zap.Error(err))
	}
	if jobsWiring != nil && jobsWiring.Module != nil {
		wiring.Jobs = jobsWiring
		registry.Register(jobsWiring.Module)
		log.Info("registered Jobs module")
	}

	// Wire Media
	mediaWiring, err := WireMedia(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire Media", zap.Error(err))
	}
	if mediaWiring != nil && mediaWiring.Module != nil {
		wiring.Media = mediaWiring
		registry.Register(mediaWiring.Module)
		log.Info("registered Media module")
	}

	// Wire Images
	imagesWiring, err := WireImages(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire Images", zap.Error(err))
	}
	if imagesWiring != nil && imagesWiring.Module != nil {
		wiring.Images = imagesWiring
		registry.Register(imagesWiring.Module)
		log.Info("registered Images module")
	}

	// Wire Drive
	driveWiring, err := WireDrive(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire Drive", zap.Error(err))
	}
	if driveWiring != nil && driveWiring.Module != nil {
		wiring.Drive = driveWiring
		registry.Register(driveWiring.Module)
		log.Info("registered Drive module")
	}

	// Wire Workflow
	workflowWiring, err := WireWorkflow(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire Workflow", zap.Error(err))
	}
	if workflowWiring != nil && workflowWiring.Module != nil {
		wiring.Workflow = workflowWiring
		registry.Register(workflowWiring.Module)
		log.Info("registered Workflow module")
	}

	// Wire Scraper
	scraperWiring, err := WireScraper(cfg, log, coreDeps)
	if err != nil {
		log.Warn("failed to wire Scraper", zap.Error(err))
	}
	if scraperWiring != nil && scraperWiring.Module != nil {
		wiring.Scraper = scraperWiring
		registry.Register(scraperWiring.Module)
		log.Info("registered Scraper module")
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
	assetsWiring, err := WireAssets(cfg, log, artlistWiring.Service, coreDeps.CatalogRepo)
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
