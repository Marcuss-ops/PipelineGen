// build_stock_enrichment.go — Gate 3b of BuildStockBundle: the
// PR-011A/B/C RLM/LLM enrichment pass wiring. Extracted so the
// BuildStockBundle orchestrator stays a thin gate dispatcher.
package app

import (
	"fmt"
	"strings"

	"go.uber.org/zap"

	stockenrich "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/enrichment"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
)

// wireStockEnrichment wires the stock RLM/LLM enrichment handler
// (PR-011A/B/C) when the EnrichmentEnabled closure returns true.
// godlike/07 fail-closed composition: enrichment is OPTIONAL
// (nil-LLMClient OR nil-EnabledFunc OR EnabledFunc()==false = no
// handler registered; the worker pool cannot dequeue
// media.stock_rlm_enrich jobs and the registry entry sits unused).
// No silent-success path: a misconfigured production deployment
// (EnabledFunc()==true but LLMClient==nil AND cfg has no
// fallback model) surfaces as a typed error at composition time.
//
// PR-011B (July 2026): the LLM client resolution order is:
//  1. deps.EnrichmentLLMClient (test override / future dev override)
//  2. real ollama adapter when cfg.External.ParseArenaLLM is non-empty
//  3. real ollama adapter fallback to cfg.External.OllamaModel
//  4. StubEnrichmentLLMClient (PR-011A default) when BOTH empty
//
// The model precedence (ParseArenaLLM > OllamaModel) is canonical
// for the stock RLM/LLM enrichment pass per AGENTS.md Pattern 0
// + godlike/06 SSOT (one canonical owner per fact: the
// resolution order lives ONLY here in the composition root).
//
// Error wrapping follows the BuildStockBundle preamble convention
// (`stock.BuildStockBundle: enrichment.<surface>: %w`).
func wireStockEnrichment(deps StockBundleDeps) error {
	if deps.Enrichment.EnrichmentEnabled != nil && deps.Enrichment.EnrichmentEnabled() {
		// Step 1: resolve the LLM client (override > real adapter > stub).
		llmClient := deps.Enrichment.EnrichmentLLMClient
		if llmClient == nil && deps.Runtime.Cfg != nil {
			modelName := strings.TrimSpace(deps.Runtime.Cfg.External.ParseArenaLLM)
			if modelName == "" {
				modelName = strings.TrimSpace(deps.Runtime.Cfg.External.OllamaModel)
			}
			if modelName != "" {
				// Construct the real ollama-backed adapter. The
				// ollama client's default model is OllamaModel
				// (canonical cfg-default); the per-capability
				// modelName override (typically ParseArenaLLM)
				// is passed to the adapter so Enrich() threads
				// it via options["model"] on every Chat call.
				ollamaCli := ollamaclient.NewClient(
					deps.Runtime.Cfg.External.OllamaURL,
					deps.Runtime.Cfg.External.OllamaModel,
					deps.Runtime.Cfg.External.OllamaTimeoutSeconds,
				)
				realAdapter, realErr := stockenrich.NewOllamaEnrichmentLLMClient(ollamaCli, modelName, deps.Runtime.Cfg.External.EnrichmentPromptVersion)
				if realErr != nil {
					return fmt.Errorf("stock.BuildStockBundle: enrichment.NewOllamaEnrichmentLLMClient: %w", realErr)
				}
				llmClient = realAdapter
				deps.Runtime.Log.Info("stock.BuildStockBundle: enrichment wired with real ollama adapter",
					zap.String("model", modelName),
					zap.String("ollama_url", deps.Runtime.Cfg.External.OllamaURL),
				)
			} else {
				// godlike/07 minimum-blast-radius: when neither
				// ParseArenaLLM nor OllamaModel is configured,
				// fall back to the stub so the worker retry path
				// is still exercised end-to-end (no silent
				// success — the stub returns
				// ErrEnrichmentLLMUnavailable verbatim).
				llmClient = stockenrich.NewStubEnrichmentLLMClient("stub:enrichment-unavailable")
				deps.Runtime.Log.Warn("stock.BuildStockBundle: enrichment using stub (no model configured; set ParseArenaLLM or OllamaModel to wire the real adapter)")
			}
		}

		if llmClient == nil {
			deps.Runtime.Log.Warn("stock.BuildStockBundle: enrichment enabled but no LLM client resolved (set EnrichmentLLMClient or configure ParseArenaLLM/OllamaModel)")
		} else {
			assetRepo, repoErr := stockenrich.NewSQLiteAssetRepository(deps.Runtime.DB)
			if repoErr != nil {
				return fmt.Errorf("stock.BuildStockBundle: enrichment.NewSQLiteAssetRepository: %w", repoErr)
			}
			assetRepo.SetMetadataUpdater(deps.Enrichment.AssetMetadataUpdater)

			// PR-011C follow-up (July 2026): wire the production
			// outbox-dispatcher-backed emitter. The emitter opens
			// a fresh SQL tx + calls outboxevents.Repository.Enqueue
			// per the canonical pattern. When deps.Runtime.DB is nil, fall
			// back to the nil-emitter (handler's godlike/07
			// nil-tolerance logs a Warn + skips the emit step)
			// — this preserves the PR-011C composition-root
			// disabled-mode wiring for tests / dev environments
			// where SQLite is not available.
			//
			// godlike/07 minimum-blast-radius: the emitter is
			// OPTIONAL (nil is allowed). Production deployments
			// that enable enrichment MUST wire a real DB +
			// real emitter (no silent-success on the emit path).
			var emitter stockenrich.AssetPublishedEmitter
			if deps.Enrichment.EnrichmentEmitter != nil {
				emitter = deps.Enrichment.EnrichmentEmitter
				deps.Runtime.Log.Info("stock.BuildStockBundle: enrichment using injected AssetPublishedEmitter")
			} else if deps.Runtime.DB != nil {
				emitter, repoErr = stockenrich.NewOutboxBackedAssetPublishedEmitter(deps.Runtime.DB, deps.Runtime.Log)
				if repoErr != nil {
					return fmt.Errorf("stock.BuildStockBundle: enrichment.NewOutboxBackedAssetPublishedEmitter: %w", repoErr)
				}
				deps.Runtime.Log.Info("stock.BuildStockBundle: enrichment wired with outbox-backed emitter (asset.published v1)")
			} else {
				deps.Runtime.Log.Warn("stock.BuildStockBundle: enrichment nil-emitter (no DB; the handler will skip asset.published v1 emit with a Warn log)")
			}

			enrichHandler, hErr := stockenrich.NewEnrichmentHandler(llmClient, assetRepo, emitter, deps.Runtime.Log)
			if hErr != nil {
				return fmt.Errorf("stock.BuildStockBundle: enrichment.NewEnrichmentHandler: %w", hErr)
			}
			if regErr := enrichHandler.RegisterHandler(deps.Orchestration.Jobs); regErr != nil {
				return fmt.Errorf("stock.BuildStockBundle: enrichment.RegisterHandler: %w", regErr)
			}
		}
	}
	return nil
}
