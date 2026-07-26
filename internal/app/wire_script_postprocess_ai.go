// Package app — wire_script_postprocess_ai.go.
//
// FASE 2.A PR3 split (July 2026): AI-backed postprocessor registration
// extracted from wire_script_postprocess.go per AGENTS.md Pattern 5
// godlike/06 SSOT one-canonical-owner-per-fact. The 4 AI-backed processors
// (entities, metadata, translation, clip_search) form
// a natural group: they all wire through Ollama/Qdrant backends with
// nil-tolerant graceful degradation.
//
// Cross-references:
//   - internal/app/wire_script_postprocess.go: registerScriptPostProcessors
//     calls registerAIBackedProcessors after inline registrations.
//   - internal/application/scripts/adapters: NewEntitiesProcessor,
//     NewMetadataProcessor, NewTranslationProcessor, NewClipSearchProcessor
//     (PR-LEGACY-UNAVAILABLE-CLIPSEARCH + PR-LEGACY-UNAVAILABLE-ENTITY-METADATA, 2026-07-10:
//     NewUnavailable* constructors no longer called from this file — processors
//     are skipped entirely when backend is absent)
//   - internal/application/scripts/usecase: NewTranslationUseCaseAdapter,
//     NewTranslationReasonClassifierAdapter, SearchArtlistClips, ClipServices
//   - internal/infrastructure/ai/ollama/adapters: NewOllamaEntityExtractorAdapter,
//     NewOllamaMetadataGeneratorAdapter
//   - internal/infrastructure/embeddings: NewOllamaEmbedderAdapter
//   - internal/infrastructure/observability: NewTranslationMetricsAdapter
//   - internal/application/scripts/ports: NewScriptTranslatorFromFunc
package app

import (
	"context"
	"fmt"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	ollamaadapters "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/prometheus/client_golang/prometheus"

	"go.uber.org/zap"
)

// registerAIBackedProcessors registers the AI-backed postprocessors:
// entities, metadata, translation, clip_search, and internet_images.
// Each processor is gated on its required infrastructure dependency
// (Ollama client for entities/metadata/translation, OllamaTranslator
// for clip_search) — when the dep
// is absent, the processor is silently skipped (BestEffort policy).
//
// Canonical ordering (per CanonicalProcessorNames):
//
//	Entities → ClipSearch → Metadata → Translation → ClipBindings → InternetImages
//
// ClipBindings is registered BEFORE this function is called (in the
// orchestrator), so this function handles the remaining 4 processors
// in their canonical positions after ClipBindings.
func registerAIBackedProcessors(
	ppReg *adapters.PostProcessorRegistry,
	root *ComposeRoot,
	cfg *config.Config,
	log *zap.Logger,
) error {
	vidrushMetrics := observability.NewVidRushMetricsAdapter()
	// ── Entities ──────────────────────────────────────────────────────
	var entityAdapter adapters.EntityExtractor
	if root.AI != nil && root.AI.ScriptGen != nil {
		if ollamaClient := root.AI.ScriptGen.GetClient(); ollamaClient != nil {
			entityAdapter = ollamaadapters.NewOllamaEntityExtractorAdapter(ollamaClient)
			log.Info("EntitiesProcessor wired with real Ollama backend (ollama.Client)")
		}
	}
	if entityAdapter == nil {
		log.Warn("EntitiesProcessor: Ollama backend not available; postprocessor not registered (entities will be skipped)")
	} else {
		if !ppReg.Register(adapters.NewEntitiesProcessor(entityAdapter, vidrushMetrics)) {
			return fmt.Errorf("register entities processor: composition bug")
		}
	}

	// ── Metadata ─────────────────────────────────────────────────────
	var metadataAdapter adapters.MetadataGenerator
	if root.AI != nil && root.AI.ScriptGen != nil {
		metadataAdapter = ollamaadapters.NewOllamaMetadataGeneratorAdapter(root.AI.ScriptGen)
		log.Info("MetadataProcessor wired with real Ollama backend (ollama.Generator)")
	}
	if metadataAdapter == nil {
		log.Warn("MetadataProcessor: Ollama backend not available; postprocessor not registered (metadata will be skipped)")
	} else {
		if !ppReg.Register(adapters.NewMetadataProcessor(metadataAdapter)) {
			return fmt.Errorf("register metadata processor: composition bug")
		}
	}

	// ── Translation ──────────────────────────────────────────────────
	if root.AI != nil && root.AI.OllamaTranslator != nil {
		translatorPort := ports.NewScriptTranslatorFromFunc(func(ctx context.Context, text, targetLanguage string) (string, error) {
			result, err := root.AI.OllamaTranslator.Translate(ctx, translation.TranslationCommand{
				Text:       text,
				TargetLang: targetLanguage,
			})
			if err != nil {
				return "", err
			}
			return result.TranslatedText, nil
		})
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

	// ── ClipSearch ───────────────────────────────────────────────────
	// ClipSearchProcessor wires OllamaTranslator so that artlist_phrases
	// extracted by EntitiesProcessor trigger actual Artlist clip searches.
	// PR-LEGACY-UNAVAILABLE-CLIPSEARCH (2026-07-10): when OllamaTranslator
	// is nil, the processor is skipped entirely — no unavailable adapter.
	var clipSearchAdapter adapters.ArtlistClipSearcher
	if root.AI != nil && root.AI.OllamaTranslator != nil {
		clipSvc := usecase.ClipServices{
			TranslationPort: root.AI.OllamaTranslator,
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
		if !ppReg.Register(adapters.NewClipSearchProcessor(clipSearchAdapter, vidrushMetrics)) {
			return fmt.Errorf("register clip_search processor: composition bug")
		}
	}

	// ── Internet images ─────────────────────────────────────────────
	if root.Domains != nil && root.Domains.ImageSearchResolver != nil {
		if !ppReg.Register(adapters.NewInternetImagesProcessor(&internetImageSearchAdapter{resolver: root.Domains.ImageSearchResolver}, vidrushMetrics)) {
			return fmt.Errorf("register internet_images processor: composition bug")
		}
		log.Info("InternetImagesProcessor wired with canonical ImageSearchResolver")
	} else {
		log.Warn("InternetImagesProcessor: ImageSearchResolver not available; postprocessor not registered (internet_images will be skipped)")
	}

	// ── Visual planning ──────────────────────────────────────────────
	// The processor owns no provider or database implementation: the
	// composition root supplies the canonical MediaMemory resolver, which
	// fans out through the shared SearchFanOut exactly once per plan.
	// Gemma is used as a ranking strategy to choose among the closed
	// candidate list returned by the resolver; on LLM failure the
	// adapter deterministically falls back to the top-scoring candidate.
	if root.Search != nil && root.Search.SearchFanOut != nil && root.DB != nil {
		resolver, err := WireMediaMemoryResolver(root.Search.SearchFanOut, root.DB.DB, log)
		if err != nil {
			return fmt.Errorf("register visual_planning: wire resolver: %w", err)
		}
		var planner adapters.VisualCandidatePlanner
		if root.AI != nil && root.AI.ScriptGen != nil {
			if ollamaClient := root.AI.ScriptGen.GetClient(); ollamaClient != nil {
				planner = ollamaadapters.NewOllamaVisualPlannerAdapter(ollamaClient, log)
				log.Info("VisualCandidatePlanner wired with real Ollama backend")
			}
		}
		if !ppReg.Register(adapters.NewVisualPlanningProcessor(resolver, planner, nil, log)) {
			return fmt.Errorf("register visual_planning processor: composition bug")
		}
		log.Info("VisualPlanningProcessor wired with canonical MediaMemory resolver")
	} else {
		log.Warn("VisualPlanningProcessor: SearchFanOut or DB not available; postprocessor not registered")
	}

	return nil
}
