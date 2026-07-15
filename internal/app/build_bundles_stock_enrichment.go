package app

import (
	"fmt"
	"strings"

	stockenrich "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/enrichment"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"go.uber.org/zap"
)

func wireStockEnrichment(deps StockBundleDeps) error {
	if deps.Enrichment.Enabled == nil || !deps.Enrichment.Enabled() {
		return nil
	}

	client := deps.Enrichment.LLMClient
	if client == nil && deps.Runtime.Cfg != nil {
		model := strings.TrimSpace(deps.Runtime.Cfg.External.ParseArenaLLM)
		if model == "" {
			model = strings.TrimSpace(deps.Runtime.Cfg.External.OllamaModel)
		}
		if model != "" {
			ollama := ollamaclient.NewClient(
				deps.Runtime.Cfg.External.OllamaURL,
				deps.Runtime.Cfg.External.OllamaModel,
				deps.Runtime.Cfg.External.OllamaTimeoutSeconds,
			)
			var err error
			client, err = stockenrich.NewOllamaEnrichmentLLMClient(
				ollama,
				model,
				deps.Runtime.Cfg.External.EnrichmentPromptVersion,
			)
			if err != nil {
				return fmt.Errorf("stock.BuildStockBundle: enrichment client: %w", err)
			}
			deps.Runtime.Log.Info("stock enrichment wired", zap.String("model", model))
		} else {
			client = stockenrich.NewStubEnrichmentLLMClient("stub:enrichment-unavailable")
			deps.Runtime.Log.Warn("stock enrichment uses unavailable stub: no model configured")
		}
	}
	if client == nil {
		return fmt.Errorf("stock.BuildStockBundle: enrichment enabled but no LLM client is available")
	}

	repo, err := stockenrich.NewSQLiteAssetRepository(deps.Persistence.DB)
	if err != nil {
		return fmt.Errorf("stock.BuildStockBundle: enrichment repository: %w", err)
	}
	emitter := deps.Enrichment.Emitter
	if emitter == nil && deps.Persistence.DB != nil {
		emitter, err = stockenrich.NewOutboxBackedAssetPublishedEmitter(deps.Persistence.DB, deps.Runtime.Log)
		if err != nil {
			return fmt.Errorf("stock.BuildStockBundle: enrichment emitter: %w", err)
		}
	}
	handler, err := stockenrich.NewEnrichmentHandler(client, repo, emitter, deps.Runtime.Log)
	if err != nil {
		return fmt.Errorf("stock.BuildStockBundle: enrichment handler: %w", err)
	}
	if err := handler.RegisterHandler(deps.Runtime.Jobs); err != nil {
		return fmt.Errorf("stock.BuildStockBundle: enrichment register handler: %w", err)
	}
	return nil
}
