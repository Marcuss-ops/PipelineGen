package app

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// Compile-time assertions for QDRANT-003 wiring.
var (
	_ clipindexer.VectorStoreIndexer = (*qdrant.IndexWriter)(nil)
)

// BuildProcessBundle builds media-processing adapters. driveUploader
// passed in directly.
//
// QDRANT-003 (June 2026): Qdrant vector-store capability reintroduced.
// IndexWriter is created and wired as the clipindexer's VectorStoreIndexer.
// EnsureSchema is deferred to wire_services.go startup plan (startup-time).
func BuildProcessBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, driveUploader *drive.Uploader) (*ProcessBundle, error) {
	_ = ctx
	mediaProcessor := initMediaProcessor(cfg, dbs.main, repos.Assets.Repository(), repos.Assets,
		repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), log, driveUploader)

	vlmClient := vlm.NewClient(vlm.Config{
		Enabled:   cfg.VLM.Enabled,
		Endpoint:  cfg.VLM.URL,
		Model:     cfg.VLM.Model,
		TimeoutMs: cfg.VLM.TimeoutMs,
		Weight:    cfg.VLM.Weight,
	})

	clipIndexerService := clipindexer.NewService(&clipindexer.Config{
		Enabled:               cfg.ClipIndexer.Enabled,
		ServerURL:             cfg.ClipIndexer.ServerURL,
		ScriptPath:            cfg.ClipIndexer.ScriptPath,
		PythonBin:             cfg.ClipIndexer.PythonBin,
		AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist,
		MaxConcurrentIndexing: cfg.ClipIndexer.MaxConcurrentIndexing,
		DBPath:                dbs.main.Path(),
	}, dbs.main, dbs.main.Path(), log)

	// QDRANT-003: wire IndexWriter as clipindexer VectorStoreIndexer.
	// Only when Qdrant is enabled AND the clip indexer is enabled.
	var collectionMgr *qdrant.CollectionManager
	var indexDeleter qdrant.QdrantDeleter
	var vectorSvc interface{}
	var qdrantClient *qdrant.Client

	if cfg.Qdrant.Enabled && clipIndexerService.IsEnabled() {
		qdrantCfg := &qdrant.Config{
			BaseURL: cfg.Qdrant.BaseURL,
			Timeout: cfg.Qdrant.Timeout,
		}
		schema := qdrant.DefaultV3Schema()
		qdrantClient = qdrant.NewClient(qdrantCfg, log)
		assetStore := qdrant.NewSQLiteAssetStore(dbs.main.DB)
		mapper := qdrant.NewPayloadMapper(assetStore, log)
		indexWriter := qdrant.NewIndexWriter(qdrantClient, schema, mapper, log)
		indexDeleter = indexWriter

		// QDRANT-004: create Searcher + SearchAdapter for the mediasearch API.
		searcher := qdrant.NewSearcher(qdrantClient, schema, log)
		searchAdapter := qdrant.NewSearchAdapter(searcher, log)
		vectorSvc = searchAdapter

		collectionMgr = qdrant.NewCollectionManager(qdrantClient, schema, log)

		clipIndexerService.SetVectorStore(indexWriter)
		log.Info("QDRANT-003: IndexWriter wired as clipindexer VectorStoreIndexer",
			zap.String("qdrant_url", cfg.Qdrant.BaseURL),
			zap.String("schema_version", schema.Version),
			zap.String("runtime_alias", schema.RuntimeAlias))
		log.Info("QDRANT-004: Searcher + SearchAdapter wired for mediasearch API")
	} else {
		log.Info("QDRANT-003: Qdrant disabled — vector store upserts will be skipped")
	}

	return &ProcessBundle{
		MediaProcessor:     mediaProcessor,
		ClipIndexerService: clipIndexerService,
		VLMClient:          vlmClient,
		CollectionManager:  collectionMgr,
		QdrantDeleter:      indexDeleter,
		VectorSvc:          vectorSvc,
		QdrantClient:       qdrantClient,
	}, nil
}
