package app

import (
	"context"
	"time"

	"go.uber.org/zap"

	sources "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/core/destination"
	"github.com/Marcuss-ops/PipelineGen/internal/core/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipcatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipresolver"
	"github.com/Marcuss-ops/PipelineGen/internal/media/ontology"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/client"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/sources/artlist"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	svcjobs "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	matchingconfig "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// ArtlistWiring holds the Artlist module wiring
type ArtlistWiring struct {
	Handler *sources.ArtlistHandler
	Module  module.Module
	Service *artlistPkg.Service
}

// WireArtlist creates the Artlist service, handler, and module
func WireArtlist(
	ctx context.Context,
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ArtlistWiring, error) {
	// 1. Lifecycle
	artlistLifecycle := wireArtlistLifecycle(coreDeps, log)

	// 2. Catalog & Indexer
	clipCatalogRepo, clipIndexerSvc := wireArtlistCatalog(ctx, cfg, coreDeps, log)

	// 3. Resolvers
	assetDestResolver := wireAssetDestinationResolver(cfg, coreDeps, log)

	// Load presets early
	presetsConfig, err := artlistPkg.LoadPresets("config/artlist_presets.yaml")
	if err != nil {
		log.Warn("failed to load artlist presets, using defaults", zap.Error(err))
	}

	// 4. Service
	artlistSvc, err := wireArtlistService(cfg, coreDeps, artlistLifecycle, assetDestResolver, clipIndexerSvc, log)
	if err != nil {
		log.Warn("Failed to create Artlist service", zap.Error(err))
		return nil, err
	}

	// 4b. Wire SemanticEnricher into artlist service
	if artlistSvc != nil && coreDeps.ClipsRepo != nil && clipIndexerSvc != nil {
		metaWriter := semantic.NewMetadataWriter(
			cfg.Paths.PythonScriptsDir,
			cfg.Storage.TempPath(),
			cfg.External.OllamaURL,
			cfg.External.OllamaModel,
			log,
		)
		enricher := artlistPkg.NewSemanticEnricher(coreDeps.ClipsRepo, clipIndexerSvc, metaWriter, coreDeps.DriveUploader, log)
		artlistSvc.SetSemanticEnricher(enricher)
		log.Info("wired semantic enricher into artlist service")
	}

	// 5. Clip Resolver
	clipResolver := wireClipResolver(cfg, coreDeps, clipCatalogRepo, presetsConfig, log)
	if clipResolver != nil {
		coreDeps.ClipResolver = clipResolver
	}

	// 6. Handler
	handler := wireArtlistHandler(cfg, artlistSvc, coreDeps, clipResolver, log)

	// 7. Module
	var mod module.Module
	if artlistSvc != nil && handler != nil {
		mod = module.NewArtlistModule(cfg, log, artlistSvc, handler)
		log.Info("created Artlist module")
	}

	return &ArtlistWiring{
		Handler: handler,
		Module:  mod,
		Service: artlistSvc,
	}, nil
}

func wireArtlistHandler(
	cfg *config.Config,
	artlistSvc *artlistPkg.Service,
	coreDeps *CoreDeps,
	clipResolver *clipresolver.Service,
	log *zap.Logger,
) *sources.ArtlistHandler {
	if artlistSvc == nil {
		return nil
	}
	return sources.NewArtlistHandler(
		artlistSvc,
		coreDeps.CatalogSyncService,
		coreDeps.JobsService,
		clipResolver,
		"node-scraper",
		log,
		cfg,
	)
}

func wireArtlistLifecycle(coreDeps *CoreDeps, log *zap.Logger) *lifecycle.Service {
	clipsRegistry := artifacts.NewClipsRegistry(
		coreDeps.DB.DB,
		coreDeps.Assets.Repository(),
		coreDeps.Assets,
		coreDeps.Assets.LocationRepository(),
		coreDeps.Assets.ProcessingRepository(),
	)
	return NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipsRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
	}, log)
}

func wireAssetDestinationResolver(cfg *config.Config, coreDeps *CoreDeps, log *zap.Logger) destination.Resolver {
	if coreDeps.DriveClient != nil {
		storageResolver := storage.NewResolver(
			storage.MediaRoot(cfg.Storage.MediaPath()),
			storage.DriveRoot(cfg.Drive.RootFolder()),
		)
		mediaStore := storage.NewStore(storageResolver, &driveutil.Uploader{Service: coreDeps.DriveClient, Log: log}, cfg.Drive.RootFolder(), "", "", cfg.Drive.SoundEffectsFolder(), log)
		return storage.NewDestinationResolver(mediaStore)
	}
	return nil
}

func wireClipResolver(cfg *config.Config, coreDeps *CoreDeps, clipCatalogRepo *clipcatalog.Repository, presetsConfig *artlistPkg.PresetsConfig, log *zap.Logger) *clipresolver.Service {
	if clipCatalogRepo == nil {
		return nil
	}

	var harvestSvc clipresolver.ArtlistHarvestService
	if coreDeps.JobsService != nil {
		harvestSvc = clipresolver.NewJobHarvestService(coreDeps.JobsService, log, presetsConfig, cfg.Drive.ArtlistFolder())
	}

	matchingCfg, err := matchingconfig.LoadMatchingConfig("config/matching.yaml")
	if err != nil {
		log.Warn("failed to load matching config, using defaults", zap.Error(err))
	}

	// Load ontology registry
	ontologyReg, err := ontology.LoadRegistry("config/ontology.yaml")
	var ontologyScorer clipresolver.OntologyScorer
	if err != nil {
		log.Warn("failed to load ontology registry", zap.Error(err))
	} else {
		ontologyScorer = ontology.NewScorer(ontologyReg)
	}

	// Create embedding provider (points to the Python server started by clipindexer)
	embedServerURL := cfg.ClipIndexer.ServerURL
	if embedServerURL == "" {
		embedServerURL = "http://127.0.0.1:8001"
	}
	embedProvider := clipresolver.NewPythonEmbeddingProvider(embedServerURL)

	// Build vector store searcher adapter if vector store is configured
	var vectorStoreSearcher clipresolver.VectorStoreSearcher
	if coreDeps.VectorStore != nil && coreDeps.VectorStore.Enabled() {
		vectorStoreSearcher = clipresolver.NewVectorStoreAdapter(coreDeps.VectorStore)
		log.Info("vector store searcher enabled for clip resolver")
	}

	// Build map of prioritized repositories
	repos := make(map[string]*clipcatalog.Repository)

	// 1. Stock database (highest priority)
	if coreDeps.ClipsRepo != nil && coreDeps.ClipsRepo.DB() != nil {
		repos["stock"] = clipcatalog.NewRepository(coreDeps.ClipsRepo.DB(), log)
		repos["stock"].SetSource("stock")
	}

	// 2. YouTube clips database
	if coreDeps.DB != nil && coreDeps.DB.DB != nil {
		repos["youtube"] = clipcatalog.NewRepository(coreDeps.DB.DB, log)
		repos["youtube"].SetSource("youtube")
	}

	// 3. Artlist database (fallback)
	repos["artlist"] = clipCatalogRepo
	repos["artlist"].SetSource("artlist")

	// Create LLM decision service for final clip evaluation
	llmCfg := clipresolver.DefaultLLMDecisionConfig()
	llmCfg.Model = cfg.External.OllamaModel
	llmCfg.Timeout = time.Duration(cfg.External.OllamaTimeoutSeconds) * time.Second

	var llmDecision *clipresolver.LLMDecisionService
	ollamaClient := client.NewClient(cfg.External.OllamaURL, cfg.External.OllamaModel, cfg.External.OllamaTimeoutSeconds)
	if ollamaClient != nil {
		llmDecision = clipresolver.NewLLMDecisionService(ollamaClient, llmCfg, log)
		log.Info("LLM decision layer enabled for clip resolver",
			zap.String("model", llmCfg.Model),
			zap.Int("top_k", llmCfg.TopK))
	}

	return clipresolver.NewService(repos, harvestSvc, embedProvider, ontologyScorer, matchingCfg, vectorStoreSearcher, llmDecision)
}

func wireArtlistService(
	cfg *config.Config,
	coreDeps *CoreDeps,
	artlistLifecycle *lifecycle.Service,
	assetDestResolver destination.Resolver,
	clipIndexerSvc *clipindexer.Service,
	log *zap.Logger,
) (*artlistPkg.Service, error) {
	artlistSvc, err := artlistPkg.NewService(
		cfg,
		coreDeps.DB.DB,
		coreDeps.DB.DB,
		coreDeps.ClipsRepo,
		coreDeps.MediaProcessor,
		artlistLifecycle,
		assetDestResolver,
		clipIndexerSvc,
		coreDeps.JobsService,
		coreDeps.DriveClient,
		coreDeps.Assets.ProcessingRepository(),
		coreDeps.Assets.VersionRepository(),
		coreDeps.Assets.LocationRepository(),
		log,
	)

	if err != nil {
		return nil, err
	}

	// Register artlist job handler
	if artlistSvc != nil && coreDeps.JobsService != nil {
		coreDeps.JobsService.RegisterHandler(svcjobs.JobTypeArtlistRun, artlistSvc.HandleJob)
		log.Info("registered artlist job handler")
	}

	return artlistSvc, nil
}
