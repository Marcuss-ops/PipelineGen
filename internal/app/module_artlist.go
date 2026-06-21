package app

import (
	"context"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	sourcesapi "github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	artsources "github.com/Marcuss-ops/PipelineGen/internal/api/sources/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	svcjobs "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipcatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipresolver"
	"github.com/Marcuss-ops/PipelineGen/internal/media/ontology"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"go.uber.org/zap"
)

// ArtlistWiring holds the Artlist module wiring.
//
// PR4d-chunk2 (June 2026): Resolver field removed. clipresolver.Service
// does not implement script.AutoHarvestService (no EnqueueHarvest method),
// so the harvest service is constructed locally in WireRegistry from
// root.Jobs.Facade (the same path used pre-PR4d). WireArtlist remains the
// canonical owner of the clipresolver construction; ArtlistWiring no longer
// needs to expose it.
type ArtlistWiring struct {
	Handler *artsources.ArtlistHandler
	Module  module.Module
	Service *artlistPkg.Service
}

// WireArtlist creates the Artlist service, handler, and module.
//
// PR4d-chunk2 (June 2026): accepts *ArtlistBundle (10 cross-bundle deps)
// + vectorStore (1 of 2 cross-bundle deps that didn't fit). Returns
// ArtlistWiring with Resolver populated so caller can use the clipresolver
// for ScriptFlow late-binding without round-tripping through CoreDeps.
func WireArtlist(ctx context.Context, cfg *config.Config, log *zap.Logger, bundle *ArtlistBundle, vectorStore *vectorstore.Service) (*ArtlistWiring, error) {
	artlistLifecycle := wireArtlistLifecycle(bundle, log)
	clipCatalogRepo, clipIndexerSvc := wireArtlistCatalog(ctx, cfg, bundle, log)
	assetDestResolver := wireAssetDestinationResolver(cfg, bundle, log)
	presetsConfig, _ := artlistPkg.LoadPresets("config/artlist_presets.yaml")
	if presetsConfig == nil {
		log.Warn("failed to load artlist presets, using defaults")
	}
	artlistSvc, err := wireArtlistService(cfg, bundle, artlistLifecycle, assetDestResolver, clipIndexerSvc, log)
	if err != nil {
		log.Warn("Failed to create Artlist service", zap.Error(err))
		return nil, err
	}
	if artlistSvc != nil && bundle.ClipsRepo != nil && clipIndexerSvc != nil {
		metaWriter := semantic.NewMetadataWriter(cfg.Paths.PythonScriptsDir, cfg.Storage.TempPath(), cfg.External.OllamaURL, cfg.External.OllamaModel, log)
		enricher := artlistPkg.NewSemanticEnricher(bundle.ClipsRepo, clipIndexerSvc, metaWriter, bundle.DriveUploader, log)
		artlistSvc.SetSemanticEnricher(enricher)
		log.Info("wired semantic enricher into artlist service")
	}
	clipResolver := wireClipResolver(cfg, bundle, clipCatalogRepo, presetsConfig, vectorStore, log)
	handler := wireArtlistHandler(cfg, artlistSvc, bundle, clipResolver, log)
	var mod module.Module
	if artlistSvc != nil && handler != nil {
		mod = sourcesapi.NewArtlistModule(cfg, log, artlistSvc, handler)
		log.Info("created Artlist module")
	}
	return &ArtlistWiring{Handler: handler, Module: mod, Service: artlistSvc}, nil
}

func wireArtlistHandler(cfg *config.Config, artlistSvc *artlistPkg.Service, bundle *ArtlistBundle, clipResolver *clipresolver.Service, log *zap.Logger) *artsources.ArtlistHandler {
	if artlistSvc == nil {
		return nil
	}
	return artsources.NewArtlistHandler(artlistSvc, bundle.CatalogSyncService, bundle.Jobs.Facade, clipResolver, "node-scraper", log, cfg)
}

func wireArtlistLifecycle(bundle *ArtlistBundle, log *zap.Logger) *lifecycle.Service {
	clipsRegistry := artifacts.NewClipsRegistry(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository())
	return NewLifecycleFromDeps(&LifecycleDeps{Registry: clipsRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService}, log)
}

func wireAssetDestinationResolver(cfg *config.Config, bundle *ArtlistBundle, log *zap.Logger) asset.Resolver {
	if bundle.DriveClient != nil {
		storageResolver := drive.NewResolver(drive.MediaRoot(cfg.Storage.MediaPath()), drive.DriveRoot(cfg.Drive.RootFolder()))
		mediaStore := drive.NewStore(storageResolver, &driveutil.Uploader{Service: bundle.DriveClient, Log: log}, cfg.Drive.RootFolder(), "", "", cfg.Drive.SoundEffectsFolder(), log)
		return drive.NewDestinationResolver(mediaStore)
	}
	return nil
}

func wireClipResolver(cfg *config.Config, bundle *ArtlistBundle, clipCatalogRepo *clipcatalog.Repository, presetsConfig *artlistPkg.PresetsConfig, vectorStore *vectorstore.Service, log *zap.Logger) *clipresolver.Service {
	if clipCatalogRepo == nil {
		return nil
	}
	var harvestSvc clipresolver.ArtlistHarvestService
	if bundle.Jobs.Service != nil {
		harvestSvc = clipresolver.NewJobHarvestService(bundle.Jobs.Facade, log, presetsConfig, cfg.Drive.ArtlistFolder())
	}
	matchingCfg, err := config.LoadMatchingConfig("config/matching.yaml")
	if err != nil {
		log.Warn("failed to load matching config, using defaults", zap.Error(err))
	}
	ontologyReg, err := ontology.LoadRegistry("config/ontology.yaml")
	var ontologyScorer clipresolver.OntologyScorer
	if err != nil {
		log.Warn("failed to load ontology registry", zap.Error(err))
	} else {
		ontologyScorer = ontology.NewScorer(ontologyReg)
	}
	embedServerURL := cfg.ClipIndexer.ServerURL
	if embedServerURL == "" {
		embedServerURL = "http://127.0.0.1:8001"
	}
	embedProvider := clipresolver.NewPythonEmbeddingProvider(embedServerURL)
	var vectorStoreSearcher clipresolver.VectorStoreSearcher
	if vectorStore != nil && vectorStore.Enabled() {
		vectorStoreSearcher = clipresolver.NewVectorStoreAdapter(vectorStore)
		log.Info("vector store searcher enabled for clip resolver")
	}
	repos := make(map[string]*clipcatalog.Repository)
	if bundle.ClipsRepo != nil && bundle.ClipsRepo.DB() != nil {
		repos["stock"] = clipcatalog.NewRepository(bundle.ClipsRepo.DB(), log)
		repos["stock"].SetSource("stock")
	}
	if bundle.DB != nil && bundle.DB.DB != nil {
		repos["youtube"] = clipcatalog.NewRepository(bundle.DB.DB, log)
		repos["youtube"].SetSource("youtube")
	}
	repos["artlist"] = clipCatalogRepo
	repos["artlist"].SetSource("artlist")
	llmCfg := clipresolver.DefaultLLMDecisionConfig()
	llmCfg.Model = cfg.External.OllamaModel
	llmCfg.Timeout = time.Duration(cfg.External.OllamaTimeoutSeconds) * time.Second
	var llmDecision *clipresolver.LLMDecisionService
	ollamaClient := client.NewClient(cfg.External.OllamaURL, cfg.External.OllamaModel, cfg.External.OllamaTimeoutSeconds)
	if ollamaClient != nil {
		llmDecision = clipresolver.NewLLMDecisionService(ollamaClient, llmCfg, log)
		log.Info("LLM decision layer enabled for clip resolver", zap.String("model", llmCfg.Model), zap.Int("top_k", llmCfg.TopK))
	}
	return clipresolver.NewService(repos, harvestSvc, embedProvider, ontologyScorer, matchingCfg, vectorStoreSearcher, llmDecision)
}

func wireArtlistService(cfg *config.Config, bundle *ArtlistBundle, artlistLifecycle *lifecycle.Service, assetDestResolver asset.Resolver, clipIndexerSvc *clipindexer.Service, log *zap.Logger) (*artlistPkg.Service, error) {
	artlistSvc, err := artlistPkg.NewService(cfg, bundle.DB.DB, bundle.DB.DB, bundle.ClipsRepo, bundle.MediaProcessor, artlistLifecycle, assetDestResolver, clipIndexerSvc, bundle.Jobs.Facade, bundle.DriveClient, bundle.Assets.ProcessingRepository(), bundle.Assets.VersionRepository(), bundle.Assets.LocationRepository(), log)
	if err != nil {
		return nil, err
	}
	if artlistSvc != nil && bundle.Jobs.Service != nil {
		bundle.Jobs.Service.RegisterHandler(svcjobs.TypeArtlistRun, artlistSvc.HandleJob)
		log.Info("registered artlist job handler")
	}
	return artlistSvc, nil
}

func wireArtlistCatalog(ctx context.Context, cfg *config.Config, bundle *ArtlistBundle, log *zap.Logger) (*clipcatalog.Repository, *clipindexer.Service) {
	if bundle.ClipIndexerService != nil {
		return clipcatalog.NewRepository(bundle.DB.DB, log), bundle.ClipIndexerService
	}
	if bundle.DB != nil && bundle.DB.DB != nil {
		if err := clipcatalog.EnsureSchema(ctx, bundle.DB.DB, log); err != nil {
			log.Warn("failed to ensure clipcatalog schema", zap.Error(err))
		}
	}
	clipCatalogRepo := clipcatalog.NewRepository(bundle.DB.DB, log)
	clipIndexerSvc := clipindexer.NewService(&clipindexer.Config{Enabled: cfg.ClipIndexer.Enabled, ServerURL: cfg.ClipIndexer.ServerURL, ScriptPath: cfg.ClipIndexer.ScriptPath, PythonBin: cfg.ClipIndexer.PythonBin, AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist, DBPath: bundle.DB.Path()}, bundle.DB.DB, bundle.DB.Path(), log)
	if err := clipIndexerSvc.StartServer(ctx); err != nil {
		log.Warn("failed to start embedding server", zap.Error(err))
	} else {
		clipIndexerSvc.StartWatchdog(ctx)
	}
	return clipCatalogRepo, clipIndexerSvc
}
