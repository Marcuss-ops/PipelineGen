// Package app — wire_script_postprocess_ai.go.
//
// FASE 2.A PR3 split (July 2026): AI-backed postprocessor registration
// extracted from wire_script_postprocess.go per AGENTS.md Pattern 5
// godlike/06 SSOT one-canonical-owner-per-fact. The AI-backed processors
// (metadata, translation, clip_search) form
// a natural group: they all wire through Ollama/Qdrant backends with
// nil-tolerant graceful degradation.
//
// Cross-references:
//   - internal/app/wire_script_postprocess.go: registerScriptPostProcessors
//     calls registerAIBackedProcessors after inline registrations.
//   - internal/capabilities/scripts/adapters: NewMetadataProcessor,
//     NewTranslationProcessor, NewClipSearchProcessor
//     (PR-LEGACY-UNAVAILABLE-CLIPSEARCH + PR-LEGACY-UNAVAILABLE-ENTITY-METADATA, 2026-07-10:
//     NewUnavailable* constructors no longer called from this file — processors
//     are skipped entirely when backend is absent)
//   - internal/capabilities/scripts/usecase: NewTranslationUseCaseAdapter,
//     NewTranslationReasonClassifierAdapter, SearchArtlistClips, ClipServices
//   - internal/platform/ollama/adapters: metadata and visual-planning adapters,
//     NewOllamaMetadataGeneratorAdapter
//   - internal/platform/embeddings: NewOllamaEmbedderAdapter
//   - internal/platform/observability: NewTranslationMetricsAdapter
//   - internal/capabilities/scripts/ports: NewScriptTranslatorFromFunc
package wiring

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providerassets"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	ollamaadapters "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/adapters"
	"github.com/prometheus/client_golang/prometheus"

	"go.uber.org/zap"
)

var _ scriptgen.SegmentProviderResolver = (*adapters.VidRushProviderFanout)(nil)
var _ scriptgen.SegmentSceneMerger = (*adapters.VidRushSceneMerger)(nil)
var _ scriptgen.VidRushMetrics = (*observability.VidRushMetricsAdapter)(nil)
var _ scriptgen.SegmentMaterializer = (*adapters.VidRushMaterializationProcessor)(nil)

// registerAIBackedProcessors registers the AI-backed postprocessors:
// metadata, translation, clip_search, and internet_images.
// Each processor is gated on its required infrastructure dependency
// (Ollama client for metadata/translation, OllamaTranslator
// for clip_search) — when the dep
// is absent, the processor is silently skipped (BestEffort policy).
//
// Canonical ordering (per CanonicalProcessorNames):
//
//	ClipSearch → Metadata → Translation → ClipBindings → InternetImages
//
// ClipBindings is registered BEFORE this function is called (in the
// orchestrator), so this function handles the remaining 4 processors
// in their canonical positions after ClipBindings.
func registerAIBackedProcessors(
	ppReg *adapters.PostProcessorRegistry,
	root *ComposeRoot,
	artlistWiring *ArtlistWiring,
	vidRushProviders *adapters.VidRushAssetProviderRegistry,
	vidRushCache ports.VidRushCachePort,
	cfg *config.Config,
	log *zap.Logger,
) error {
	vidrushMetrics := observability.NewVidRushMetricsAdapter()
	ppReg.SetVidRushTimingMetrics(vidrushMetrics)
	// ── Metadata ─────────────────────────────────────────────────────
	var metadataAdapter adapters.MetadataGenerator

	if root.AI != nil && root.AI.ScriptGen != nil {
		metadataAdapter = ollamaadapters.NewOllamaMetadataGeneratorAdapter(
			root.AI.ScriptGen,
		)
		log.Info("MetadataProcessor wired with optional Ollama backend")
	} else {
		log.Info(
			"MetadataProcessor wired without Ollama; caller-provided metadata remains available",
		)
	}

	if !ppReg.Register(adapters.NewMetadataProcessor(metadataAdapter)) {
		return fmt.Errorf("register metadata processor: composition bug")
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
	// ClipSearchProcessor can use the canonical VidRush provider registry
	// directly. Manual media_plan.searches and locally extracted phrases do
	// not require Ollama translation; only the legacy ClipServices fallback
	// does.
	var clipSearchAdapter adapters.ArtlistClipSearcher
	var registryMediaResolver *adapters.VidRushRegistryMediaResolver
	if vidRushProviders != nil {
		if _, err := vidRushProviders.Provider(scriptpkg.VidRushProviderArtlist); err == nil {
			registryMediaResolver = &adapters.VidRushRegistryMediaResolver{Registry: vidRushProviders}
			clipSearchAdapter = registryMediaResolver
			log.Info("ClipSearchProcessor wired through VidRushAssetProviderRegistry")
		}
	}
	if clipSearchAdapter == nil && root.AI != nil && root.AI.OllamaTranslator != nil {
		clipSvc := usecase.ClipServices{
			TranslationPort: root.AI.OllamaTranslator,
			Logger:          log,
			ArtlistFolder:   cfg.Drive.ArtlistFolder(),
		}
		if root.Repos != nil && root.Repos.ClipsRepo != nil {
			clipSvc.RealtimeSvc = &sqliteRealtimeSearchAdapter{repo: root.Repos.ClipsRepo}
		}
		if root.Drive != nil && root.Drive.DriveUploader != nil {
			clipSvc.DriveSvc = &driveCheckServiceAdapter{
				up: root.Drive.DriveUploader,
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
		if clipSearchAdapter == nil {
			remoteClipSearchAdapter := &artlistClipSearchAdapter{svc: clipSvc}
			if artlistWiring != nil && artlistWiring.ProviderAssets != nil {
				remoteClipSearchAdapter.remoteSearch = func(ctx context.Context, req providerassets.SearchRequest) (providerassets.SearchResult, error) {
					return artlistWiring.ProviderAssets.Search(ctx, "artlist", req)
				}
				log.Info("ClipSearchProcessor wired through the canonical remote Artlist provider registry")
			}
			clipSearchAdapter = remoteClipSearchAdapter
		}
		log.Info("ClipSearchProcessor wired with rich ClipServices",
			zap.Bool("drive_svc", clipSvc.DriveSvc != nil),
			zap.Bool("jobs_svc", clipSvc.JobsSvc != nil),
			zap.Bool("assoc_svc", clipSvc.AssocSvc != nil),
			zap.Bool("realtime_svc", clipSvc.RealtimeSvc != nil),
			zap.Bool("artlist_folder", clipSvc.ArtlistFolder != ""),
		)
	}
	if clipSearchAdapter == nil {
		log.Warn("ClipSearchProcessor: Artlist searcher not available; postprocessor not registered (clip_search will be skipped)")
	} else {
		if !ppReg.Register(adapters.NewClipSearchProcessorWithCache(clipSearchAdapter, vidRushCache, vidrushMetrics)) {
			return fmt.Errorf("register clip_search processor: composition bug")
		}
	}

	// Internet image discovery is owned by the unified VidRush MediaResolver
	// and its Local Stock/MediaSampler chain. The former standalone
	// Standalone image processor is intentionally not registered here, avoiding a
	// second provider pipeline in production.

	// ── Visual planning ──────────────────────────────────────────────
	// The processor owns no provider or database implementation: the
	// composition root supplies the canonical MediaMemory resolver, which
	// fans out through the shared SearchFanOut exactly once per plan.
	// Gemma is used as a ranking strategy to choose among the closed
	// candidate list returned by the resolver; on LLM failure the
	// adapter deterministically falls back to the top-scoring candidate.
	var visualPlanner adapters.VisualCandidatePlanner
	if root.Search != nil && root.Search.SearchFanOut != nil && root.DB != nil {
		resolver, err := WireMediaMemoryResolver(root.Search.SearchFanOut, root.DB.DB, log)
		if err != nil {
			return fmt.Errorf("register visual_planning: wire resolver: %w", err)
		}
		if root.AI != nil && root.AI.ScriptGen != nil {
			if ollamaClient := root.AI.ScriptGen.GetClient(); ollamaClient != nil {
				visualPlanner = ollamaadapters.NewOllamaVisualPlannerAdapter(ollamaClient, log)
				log.Info("VisualCandidatePlanner wired with real Ollama backend")
			}
		}
		if !ppReg.Register(adapters.NewVisualPlanningProcessor(resolver, visualPlanner, nil, log)) {
			return fmt.Errorf("register visual_planning processor: composition bug")
		}
		log.Info("VisualPlanningProcessor wired with canonical MediaMemory resolver")
	} else {
		log.Warn("VisualPlanningProcessor: SearchFanOut or DB not available; postprocessor not registered")
	}
	// Timeline slots are resolver-local and must remain available for manual
	// plans even when the external MediaMemory resolver is unavailable.
	if !ppReg.Register(adapters.NewVisualSlotsProcessor(adapters.NewClosedVisualPlanner(visualPlanner))) {
		return fmt.Errorf("register visual_slots processor: composition bug")
	}

	return nil
}
