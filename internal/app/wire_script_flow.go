package app

import (
	"go.uber.org/zap"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/content/mediacurator"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/scripts"
)

// wireScriptFlowExtras wires the optional clip-source builder and media curator
// into a ScriptFlowHandler.  This is the single place where the dependency
// checks, logging, and service construction live so that both the registry
// path (HTTP routes) and the compose-integration path (job handlers) stay in
// sync.
func wireScriptFlowExtras(
	handler *scriptpkg.ScriptFlowHandler,
	ollamaClient *client.Client,
	vectorStore *vectorstore.Service,
	clipsOnlyRepo *clips.Repository,
	engine *scriptcore.Engine,
	cfg *config.Config,
	log *zap.Logger,
) {
	if ollamaClient == nil {
		log.Info("ollama client not available, skipping clip source builder wiring")
		return
	}

	clipSourceBuilder := scriptcore.NewClipSourceBuilder(clipsOnlyRepo, ollamaClient, log)
	if vectorStore != nil && cfg.Features.CatalogScriptVectorSearch {
		clipSourceBuilder.SetVectorStore(vectorStore)
		log.Info("vector store wired into clip source builder for semantic catalog search")
	} else if vectorStore != nil && !cfg.Features.CatalogScriptVectorSearch {
		log.Info("vector store available but catalog script vector search disabled via config")
	}
	if cfg.Reranker.Enabled {
		rerankerCli := reranker.NewClient(reranker.Config{
			Enabled:   cfg.Reranker.Enabled,
			URL:       cfg.Reranker.URL,
			Model:     cfg.Reranker.Model,
			TopK:      cfg.Reranker.TopK,
			TimeoutMs: cfg.Reranker.TimeoutMs,
		})
		clipSourceBuilder.SetReranker(rerankerCli)
		log.Info("reranker wired into clip source builder for catalog result reordering")
	}
	handler.SetClipSourceBuilder(clipSourceBuilder)
	log.Info("clip source builder initialized for Clip→Script and Catalog→Script generation")

	// Wire ClipSourceBuilder into the curation service for GenerateFromCatalog endpoint.
	// The CurationService is created before wireScriptFlowExtras with nil ClipSourceBuilder
	// (late-binding pattern — same as how it's done on ScriptFlowHandler).
	handler.SetCurationClipSourceBuilder(clipSourceBuilder)

	if (vectorStore != nil || clipsOnlyRepo != nil) && engine != nil {
		embedderURL := cfg.ClipIndexer.ServerURL
		mediaCurator := mediacurator.NewService(vectorStore, embedderURL, clipsOnlyRepo, clipSourceBuilder, engine, log)
		handler.SetMediaCurator(mediaCurator)
		log.Info("media curator initialized",
			zap.String("embedder_url", embedderURL))
	} else {
		log.Warn("media curator not initialized: missing dependencies",
			zap.Bool("vectorstore", vectorStore != nil),
			zap.Bool("engine", engine != nil))
	}
}
