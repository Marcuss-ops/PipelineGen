// Package app — wire_script_postprocess_ai.go.
//
// FASE 2.A PR3 split (July 2026): AI-backed postprocessor registration
// extracted from wire_script_postprocess.go per AGENTS.md Pattern 5
// godlike/06 SSOT one-canonical-owner-per-fact. The 5 AI-backed processors
// (entities, metadata, translation, stock_association, clip_search) form
// a natural group: they all wire through Ollama/Qdrant backends with
// nil-tolerant graceful degradation.
//
// Cross-references:
//   - internal/app/wire_script_postprocess.go: registerScriptPostProcessors
//     calls registerAIBackedProcessors after inline registrations.
//   - internal/application/scripts/adapters: NewEntitiesProcessor,
//     NewMetadataProcessor, NewTranslationProcessor, NewStockAssociationProcessor,
//     NewClipSearchProcessor, NewUnavailableEntityExtractionAdapter,
//     NewUnavailableMetadataGenerationAdapter, NewUnavailableArtlistClipSearcher
//   - internal/application/scripts/usecase: NewTranslationUseCaseAdapter,
//     NewTranslationReasonClassifierAdapter, SearchArtlistClips, ClipServices
//   - internal/infrastructure/ai/ollama/adapters: NewOllamaEntityExtractorAdapter,
//     NewOllamaMetadataGeneratorAdapter
//   - internal/infrastructure/embeddings: NewOllamaEmbedderAdapter
//   - internal/infrastructure/observability: NewTranslationMetricsAdapter
//   - internal/infrastructure/qdrant/search: NewTextEmbedderAdapter,
//     NewStockSearchAdapter
//   - internal/application/scripts/ports: NewScriptTranslatorFromFunc
package app

import (
	"fmt"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	ollamaadapters "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/embeddings"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/prometheus/client_golang/prometheus"

	"go.uber.org/zap"
)

// registerAIBackedProcessors registers the 5 AI-backed postprocessors:
// entities, metadata, translation, stock_association, and clip_search.
// Each processor is gated on its required infrastructure dependency
// (Ollama client for entities/metadata/translation, Qdrant+Ollama for
// stock_association, OllamaTranslator for clip_search) — when the dep
// is absent, the processor is silently skipped (BestEffort policy).
//
// Canonical ordering (per CanonicalProcessorNames):
//
//	Entities → Metadata → Translation → ClipBindings → StockAssociation → ClipSearch
//
// ClipBindings is registered BEFORE this function is called (in the
// orchestrator), so this function handles the remaining 5 processors
// in their canonical positions after ClipBindings.
func registerAIBackedProcessors(
	ppReg *adapters.PostProcessorRegistry,
	root *ComposeRoot,
	cfg *config.Config,
	log *zap.Logger,
	metaModel string,
) error {
	// ── Entities ──────────────────────────────────────────────────────
	var entityAdapter adapters.EntityExtractor
	if root.AI != nil && root.AI.ScriptGen != nil {
		if ollamaClient := root.AI.ScriptGen.GetClient(); ollamaClient != nil {
			entityAdapter = ollamaadapters.NewOllamaEntityExtractorAdapter(ollamaClient)
			log.Info("EntitiesProcessor wired with real Ollama backend (ollama.Client)")
		}
	}
	if entityAdapter == nil {
		entityAdapter = adapters.NewUnavailableEntityExtractionAdapter()
		log.Warn("EntitiesProcessor: Ollama backend not available; falling back to unavailable adapter (entities will produce warnings)")
	}
	if !ppReg.Register(adapters.NewEntitiesProcessor(entityAdapter)) {
		return fmt.Errorf("register entities processor: composition bug")
	}

	// ── Metadata ─────────────────────────────────────────────────────
	var metadataAdapter adapters.MetadataGenerator
	if root.AI != nil && root.AI.ScriptGen != nil {
		metadataAdapter = ollamaadapters.NewOllamaMetadataGeneratorAdapter(root.AI.ScriptGen)
		log.Info("MetadataProcessor wired with real Ollama backend (ollama.Generator)")
	}
	if metadataAdapter == nil {
		metadataAdapter = adapters.NewUnavailableMetadataGenerationAdapter()
		log.Warn("MetadataProcessor: Ollama backend not available; falling back to unavailable adapter (metadata will produce warnings)")
	}
	if !ppReg.Register(adapters.NewMetadataProcessor(metadataAdapter)) {
		return fmt.Errorf("register metadata processor: composition bug")
	}

	// ── Translation ──────────────────────────────────────────────────
	if root.AI != nil && root.AI.OllamaTranslator != nil {
		translatorPort := ports.NewScriptTranslatorFromFunc(root.AI.OllamaTranslator.TranslateText)
		metricsPort, mErr := observability.NewTranslationMetricsAdapter(prometheus.DefaultRegisterer)
		if mErr != nil {
			return fmt.Errorf("register translation processor: metrics adapter: %w", mErr)
		}
		transProc := adapters.NewTranslationProcessor(
			translatorPort,
			metricsPort,
			usecase.NewTranslationUseCaseAdapter(),
			usecase.NewTranslationReasonClassifierAdapter(),
			log,
		)
		if !ppReg.Register(transProc) {
			return fmt.Errorf("register translation processor: composition bug")
		}
		log.Info("TranslationProcessor wired (OllamaTranslator + Prometheus metrics adapter)")
	} else {
		log.Warn("TranslationProcessor: OllamaTranslator not available; postprocessor not registered (translation requests will produce warnings)")
	}

	// ── StockAssociation ─────────────────────────────────────────────
	// Wraps Qdrant searcher for per-scene vector search over stock-indexed
	// assets. BestEffort policy: a missing or failing stock search does
	// not block the pipeline.
	if root.AI != nil && root.AI.ScriptGen != nil &&
		root.Process != nil && root.Process.QdrantSearcher != nil {
		if ollamaClient := root.AI.ScriptGen.GetClient(); ollamaClient != nil {
			embedder := search.NewTextEmbedderAdapter(embeddings.NewOllamaEmbedderAdapter(ollamaClient))
			stockSearchPort := search.NewStockSearchAdapter(root.Process.QdrantSearcher, embedder, "text", log)
			if !ppReg.Register(adapters.NewStockAssociationProcessor(stockSearchPort, log)) {
				return fmt.Errorf("register stock_association processor: composition bug")
			}
			log.Info("StockAssociationProcessor wired (Qdrant + Ollama embedder)")
		}
	}

	// ── ClipSearch ───────────────────────────────────────────────────
	// ClipSearchProcessor wires OllamaTranslator so that artlist_phrases
	// extracted by EntitiesProcessor trigger actual Artlist clip searches.
	// Falls back to unavailable adapter when root.AI.OllamaTranslator
	// is not wired.
	var clipSearchAdapter adapters.ArtlistClipSearcher
	if root.AI != nil && root.AI.OllamaTranslator != nil {
		clipSvc := usecase.ClipServices{
			TranslationPort: root.AI.OllamaTranslator,
			MetadataModel:   metaModel,
			Logger:          log,
			ArtlistFolder:   cfg.Drive.ArtlistFolder(),
		}
		if root.Drive != nil && root.Drive.driveUploader != nil {
			clipSvc.DriveSvc = &driveCheckServiceAdapter{
				up: root.Drive.driveUploader,
			}
		}
		if root.Jobs != nil && root.Jobs.Service != nil {
			clipSvc.JobsSvc = &jobsEnqueueServiceAdapter{
				svc: root.Jobs.Service,
			}
		}
		if root.Domains != nil && root.Domains.AssocService != nil {
			clipSvc.AssocSvc = root.Domains.AssocService
		}
		clipSearchAdapter = &artlistClipSearchAdapter{
			svc: clipSvc,
		}
		log.Info("ClipSearchProcessor wired with rich ClipServices",
			zap.Bool("drive_svc", clipSvc.DriveSvc != nil),
			zap.Bool("jobs_svc", clipSvc.JobsSvc != nil),
			zap.Bool("assoc_svc", clipSvc.AssocSvc != nil),
			zap.Bool("artlist_folder", clipSvc.ArtlistFolder != ""),
		)
	}
	if clipSearchAdapter == nil {
		log.Warn("ClipSearchProcessor: OllamaTranslator not available; postprocessor not registered (clip_search will be skipped)")
	} else {
		if !ppReg.Register(adapters.NewClipSearchProcessor(clipSearchAdapter)) {
			return fmt.Errorf("register clip_search processor: composition bug")
		}
	}

	return nil
}
