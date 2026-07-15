package app

import (
	"fmt"
	"strings"

	stockenrich "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/enrichment"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"go.uber.org/zap"
)

func wireStockEnrichment(deps StockBundleDeps) error {
	if deps.EnrichmentEnabled == nil || !deps.EnrichmentEnabled() {
		return nil
	}

	llmClient := deps.EnrichmentLLMClient
	if llmClient == nil && deps.Cfg != nil {
		modelName := strings.TrimSpace(deps.Cfg.External.ParseArenaLLM)
		if modelName == "" {
			modelName = strings.TrimSpace(deps.Cfg.External.OllamaModel)
		}
		if modelName != "" {
			ollamaClient := ollamaclient.NewClient(
				deps.Cfg.External.OllamaURL,
				deps.Cfg.External.OllamaModel,
				deps.Cfg.External.OllamaTimeoutSeconds,
			)
			realAdapter, err := stockenrich.NewOllamaEnrichmentLLMClient(
				ollamaClient,
				modelName,
				deps.Cfg.External.EnrichmentPromptVersion,
			)
			if err != nil {
				return fmt.Errorf("stock.BuildStockBundle: enrichment.NewOllamaEnrichmentLLMClient: %w", err)
			}
			llmClient = realAdapter
			deps.Log.Info("stock.BuildStockBundle: enrichment wired with real ollama adapter",
				zap.String("model", modelName),
				zap.String("ollama_url", deps.Cfg.External.OllamaURL),
			)
		} else {
			llmClient = stockenrich.NewStubEnrichmentLLMClient("stub:enrichment-unavailable")
			deps.Log.Warn("stock.BuildStockBundle: enrichment using stub (no model configured; set ParseArenaLLM or OllamaModel to wire the real adapter)")
		}
	}

	if llmClient == nil {
		return nil
	}

	assetRepo, err := stockenrich.NewSQLiteAssetRepository(deps.DB)
	if err != nil {
		return fmt.Errorf("stock.BuildStockBundle: enrichment.NewSQLiteAssetRepository: %w", err)
	}

	var emitter stockenrich.AssetPublishedEmitter
	if deps.DB != nil {
		emitter, err = stockenrich.NewOutboxBackedAssetPublishedEmitter(deps.DB, deps.Log)
		if err != nil {
			return fmt.Errorf("stock.BuildStockBundle: enrichment.NewOutboxBackedAssetPublishedEmitter: %w", err)
		}
		deps.Log.Info("stock.BuildStockBundle: enrichment wired with outbox-backed emitter (asset.published v1)")
	} else {
		deps.Log.Warn("stock.BuildStockBundle: enrichment nil-emitter (no DB; the handler will skip asset.published v1 emit with a Warn log)")
	}

	handler, err := stockenrich.NewEnrichmentHandler(llmClient, assetRepo, emitter, deps.Log)
	if err != nil {
		return fmt.Errorf("stock.BuildStockBundle: enrichment.NewEnrichmentHandler: %w", err)
	}
	if err := handler.RegisterHandler(deps.Jobs); err != nil {
		return fmt.Errorf("stock.BuildStockBundle: enrichment.RegisterHandler: %w", err)
	}
	return nil
}
