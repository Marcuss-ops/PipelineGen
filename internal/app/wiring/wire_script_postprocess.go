// Package app — wire_script_postprocess.go.
//
// FASE 2.A PR3 (June 2026) split: the post-processor registration block
// moved out of wire_script.go. The block was previously inline in
// wireScriptFlow (pre-PR3 lines ~218-353), interleaving ppReg
// construction + 7 sequential Register calls + the freeze step into
// the orchestrator. Extracting it to a dedicated helper function
// returns the orchestrator to a pure-routing shape (use cases →
// job handler → handler → module.Register) and groups the canonical
// 10 postprocessors into a single testable seam.
//
// Package boundary: same `package app` as wire_script.go, exactly
// mirroring the wire_script_sources.go / wire_script_curation.go
// precedent. Caller is wireScriptFlow; the function takes the minimal
// 6-parameter contract.
//
// Cross-references:
//   - internal/app/wire_script.go: the caller (wireScriptFlow
//     invokes registerScriptPostProcessors immediately after ppReg
//     construction).
//   - internal/app/wire_script_postprocess_ai.go: registerAIBackedProcessors
//   - internal/app/wire_script_adapters.go: composition-time
//     validators that operate on the post-freeze ppReg.
//   - internal/app/wire_script_curation.go: imageGenSvcAdapter
//     (composition-root-local adapter the image processor wraps).
package wiring

import (
	"context"
	"fmt"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	processor "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"

	"go.uber.org/zap"
)
import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	ollamaadapters "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/adapters"
	"github.com/prometheus/client_golang/prometheus"
)

// pgMediaAssetLookupAdapter satisfies drive.AssetStoreLookup against the
// PostgreSQL media SSOT. It is the ONLY deep LocationVerifier cross-reference.
//
// A SQLite-backed sibling (assetServiceLookupAdapter, wrapping detail.Service)
// was deleted on 2026-09-13: RequireMediaPostgres states that an enabled media
// plane "means PostgreSQL is mandatory ... There is no SQLite/Qdrant
// fallback", so a second deep verifier reading the SQLite mirror re-introduced
// the split-brain this cutover removed — a PostgreSQL-committed asset looked
// like an orphan to it. With a closed media plane the processor falls through
// to the explicit Drive-only shallow verifier instead of a stale catalog.
type pgMediaAssetLookupAdapter struct {
	reader *pgmedia.MediaSearcher
}

func (a *pgMediaAssetLookupAdapter) GetAsset(
	ctx context.Context, assetID string,
) (*asset.Details, error) {
	if a == nil || a.reader == nil {
		return nil, fmt.Errorf("asset lookup: postgres media reader not wired")
	}
	return a.reader.GetAssetDetails(ctx, assetID)
}

// registerScriptPostProcessors initialises and registers every
// canonical postprocessor on the supplied registry. Each registration
// is gated on its required infrastructure dependency (VoiceoverService
// for voiceover, QdrantSearcher + OllamaClient for clip_search).
//
// SCRIPTCONTRACT-2026-07-08 PR-1 canonical order (godlike/06 SSOT):
//
//	Persistence → Voiceover → ClipBindings → StockBindings →
//	NarrationSanitizer → AssetLocationReconciliation → VidRushMaterialization
//
// Persistence is FIRST so no Drive-write side effect runs before the
// local SQLite row is locked. An earlier order ran the document leg
// before Persistence and caused RETRY_WAIT 90% on a Drive-write-
// success + DB-persist-failure cascade (job retries re-triggered
// Drive writes on already-created artifacts; idempotency was only
// event-outbox, not artifact-publish). TranslationProcessor slots
// between Metadata and ClipBindings so the translated SpecScene is
// consumed by the downstream ClipBindings pass (PR-TRANSLATE-SCRIPT-SPEC
// FP2, 2026-08-08). Forward-prevention gate:
// scripts/ci-architectural-checks.sh `Check 64` locks this 10-processor
// REGISTRATION order.
// The freeze step remains in wireScriptFlow so the orchestrator owns
// the freeze + post-freeze invariant ordering (matches the
// postProcessorRegistry precedent in the canonical pipeline composer).
//
// Returns the FIRST error encountered on a Register call so the
// orchestrator can wrap it with the "wireScriptFlow:" prefix and
// fail-closed per AGENTS.md Pattern 8. Composition-bug errors
// (duplicate name, malformed processor-ctor) propagate unchanged;
// caller-supplied dependencies that are missing at composition are
// silently skipped (graceful-degradation per spec).
func registerScriptPostProcessors(
	ppReg *adapters.PostProcessorRegistry,
	root *ComposeRoot,
	cfg *config.Config,
	log *zap.Logger,
	scriptsRepoAdapter ports.ScriptRepository,
	metaModel string,
	searchFanOut assetsearch.SearchFanOut,
	vidRushProviders *adapters.VidRushAssetProviderRegistry,
	vidRushFinalizer ports.VidRushArtifactFinalizer,
	vidRushCache ports.VidRushCachePort,
) error {
	if ppReg == nil {
		return fmt.Errorf("registerScriptPostProcessors: ppReg is nil (composition bug)")
	}

	// SCRIPTCONTRACT-2026-07-08 PR-1: PersistenceProcessor moved to
	// FIRST slot in this function body. The canonical reasoning is
	// that NO Drive-write side effect may run before the local
	// SQLite row is persisted — this prevents RETRY_WAIT-90% on a
	// Drive-write-success + DB-persist-failure cascade.
	//
	// Owner: PersistenceProcessor is the SOLE canonical owner of
	// scripts/script_assets SQLite row writes post FASE PR-5.
	if scriptsRepoAdapter != nil {
		if !ppReg.Register(processor.NewPersistenceProcessor(scriptsRepoAdapter, log)) {
			return fmt.Errorf("register persistence processor: composition bug or duplicate name")
		}
	}

	// Fase 2 Spina Dorsale (July 2026) — re-enabled 2026-07-08:
	// inline voiceover postprocessor coexists with the downstream
	// voiceover.generate job.
	//
	// P0-#3 final closure (July 2026): the processor now consumes the
	// canonical voiceover.VoiceoverItemExecutor port (the per-item
	// use case the voiceover.generate_item child job + the
	// promoVoiceoverAdapter already route through). The composition
	// root passes `root.Domains.VoiceoverProcessItem` (the
	// *ProcessVoiceoverItemUseCase concrete wired in
	// build_bundles_voiceover.go), NOT the legacy
	// `root.Domains.VoiceoverService`. The VoiceoverService bundle
	// field is RETAINED for downstream consumers (jobs.voiceover
	// async paths, the `processLanguage` batch fallback) that still
	// depend on the legacy Generate/GenerateWithDestination port,
	// but the inline postprocessor path is now exclusively the
	// narrow-port surface.
	if root.Domains != nil && root.Domains.VoiceoverProcessItem != nil {
		voProc := processor.NewVoiceoverProcessor(root.Domains.VoiceoverProcessItem, log)
		voiceMap := make(map[string]string)
		if registry, registryErr := BuildLanguageRegistry(ActiveMultilingualConfig(cfg)); registryErr == nil {
			for _, spec := range registry.EnabledLanguages() {
				if spec.EdgeTTSVoice != "" {
					voiceMap[spec.Code] = spec.EdgeTTSVoice
				}
			}
		} else {
			log.Warn("VoiceoverProcessor: multilingual voice registry unavailable", zap.Error(registryErr))
		}
		var translatorPort ports.ScriptTranslator
		if root.AI != nil && root.AI.OllamaTranslator != nil {
			translatorPort = ports.NewScriptTranslatorFromFunc(func(ctx context.Context, text, targetLanguage string) (string, error) {
				result, err := root.AI.OllamaTranslator.Translate(ctx, translation.TranslationCommand{
					Text: text, TargetLang: targetLanguage,
				})
				if err != nil {
					return "", err
				}
				return result.TranslatedText, nil
			})
		}
		voProc.ConfigureDefaults(cfg.Scripts.DefaultLanguage)
		voProc.ConfigureMultilingual(voiceMap, translatorPort)
		if !ppReg.Register(voProc) {
			return fmt.Errorf("register voiceover processor: composition bug or duplicate name")
		}
		log.Info("VoiceoverProcessor (inline scene voiceovers) successfully registered (P0-#3: consumes VoiceoverItemExecutor port)")
	}

	// PR 7 (June 2026): register ClipBindingsProcessor so the
	// postprocessor walk produces ONE canonical set of scene-clip
	// bindings consumed by the JSON response writer (via
	// result.Output.SpecScene.Scenes). BestEffort policy.
	if !ppReg.Register(processor.NewClipBindingsProcessor(log)) {
		return fmt.Errorf("register clip_bindings processor: composition bug")
	}
	if !ppReg.Register(processor.NewStockBindingsProcessor()) {
		return fmt.Errorf("register stock_bindings processor: composition bug")
	}
	if !ppReg.Register(processor.NewNarrationSanitizer()) {
		return fmt.Errorf("register narration_sanitizer processor: composition bug")
	}
	// AssetLocationReconciliationProcessor verifies every drive_link
	// in the SpecScene bindings before the document is published.
	// BestEffort policy: transport errors become warnings, link
	// integrity is best-effort.
	//
	// When the media SSOT is open, the processor uses the deep
	// LocationVerifier, which cross-references Drive API results against
	// PostgreSQL media_assets to detect ORPHAN_DRIVE_FILE,
	// BROKEN_ASSET_LOCATION, and DUPLICATE states.
	//
	// When it is closed, it falls back to the lighter
	// AssetLocationResolverAdapter (Drive-only: MISSING, TRASHED,
	// INACCESSIBLE, VERIFIED, UPDATED) — the ONLY second mode, because a
	// SQLite-backed deep verifier would cross-reference a different catalog.
	if root != nil && root.Drive != nil && root.Drive.Reader != nil {
		var verifier scriptpkg.AssetLocationVerifier
		if root.MediaPostgres != nil {
			// MEDIA-SSOT: media facts live in PostgreSQL, so the deep verifier
			// cross-references Drive against the media SSOT — never against the
			// SQLite mirror (a PostgreSQL-committed asset looked like an orphan).
			adapter := &pgMediaAssetLookupAdapter{reader: pgmedia.NewMediaSearcher(root.MediaPostgres)}
			verifier = drive.NewLocationVerifier(root.Drive.Reader, adapter)
			log.Info("AssetLocationReconciliationProcessor (BestEffort, deep: Drive+PostgreSQL) successfully registered")
		} else {
			verifier = drive.NewAssetLocationResolverAdapter(root.Drive.Reader)
			log.Info("AssetLocationReconciliationProcessor (BestEffort, shallow: Drive-only) successfully registered")
		}
		var locationProcessor *processor.AssetLocationReconciliationProcessor
		if root != nil && root.CanonicalAssetWriter != nil {
			mutator, ok := root.CanonicalAssetWriter.(persistence.AssetMutator)
			if !ok || mutator == nil {
				return fmt.Errorf("register asset_location_reconciliation processor: canonical writer does not implement AssetMutator")
			}
			locationProcessor = processor.NewDurableAssetLocationReconciliationProcessor(verifier, mutator)
		} else {
			locationProcessor = processor.NewAssetLocationReconciliationProcessor(verifier)
		}
		if !ppReg.Register(locationProcessor) {
			return fmt.Errorf("register asset_location_reconciliation processor: composition bug or duplicate name")
		}
	}

	// VidRush provider registry + cache are built once by the orchestrator
	// (wireScriptFlow) and shared here with both the AI-backed processors and
	// the durable incremental runtime. Providers remain unavailable when their
	// concrete dependencies are absent; no empty-success adapter is registered.

	// AI-backed processors (entities, metadata, translation,
	// visual_planning, clip_search) — see wire_script_postprocess_ai.go.
	if err := registerAIBackedProcessors(ppReg, root, searchFanOut, vidRushProviders, vidRushCache, cfg, log); err != nil {
		return err
	}
	// VidRush search processors only discover remote candidates. The shared
	// materialization phase owns Acquire → Verify → Drive/SQLite/outbox.
	// Register the processor even when concrete dependencies are unavailable:
	// inactive plans remain compatible, while an active VidRush plan fails
	// closed at runtime instead of becoming a successful no-op.
	var entityImageCatalogRepo entitycatalog.Repository
	if root.Repos != nil {
		entityImageCatalogRepo = root.Repos.EntityImageCatalog
	}
	if !ppReg.Register(adapters.NewVidRushMaterializationProcessorWithCatalog(vidRushProviders, vidRushFinalizer, vidRushCache, entityImageCatalogRepo, ppReg.TimingMetrics())) {
		return fmt.Errorf("register vidrush materialization processor: composition bug")
	}
	log.Info("VidRushMaterializationProcessor wired through the canonical provider registry")

	return nil
}

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
	searchFanOut search.SearchFanOut,
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

	if !ppReg.Register(processor.NewMetadataProcessor(metadataAdapter)) {
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
		transProc := processor.NewTranslationProcessor(
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

	// Internet image discovery is the first half of the unified VidRush media
	// path. It discovers and caches candidates; VidRushMaterialization owns
	// Acquire → Verify → Drive/SQLite/outbox. Keeping this processor registered
	// is required because the generation plan explicitly requests the canonical
	// `internet_images` stage before materialization.
	imageSearcher := vidrushInternetImageSearcher(root, log)
	if imageSearcher != nil {
		var entityImageCatalogRepo entitycatalog.Repository

		if root != nil && root.Repos != nil {
			entityImageCatalogRepo = root.Repos.EntityImageCatalog
		}
		imageStage := adapters.NewMediaResolverImageStageWithCatalog(imageSearcher, vidRushCache, entityImageCatalogRepo, vidrushMetrics)
		// Opt-in media_manifest fast-path: when VELOX_MEDIA_MANIFEST_PATH points
		// at a verified manifest, pinned segment candidates bypass cache tiers
		// and provider search entirely (zero network calls per run). Fail-open:
		// a broken manifest must never fail a run.
		if pins, pinsErr := adapters.LoadMediaManifestPinsFromEnv(); pinsErr != nil {
			log.Warn("InternetImagesProcessor media_manifest fast-path disabled: manifest load failed", zap.String("error", pinsErr.Error()))
		} else if pins != nil {
			imageStage = imageStage.WithMediaManifestPins(pins)
			log.Info("InternetImagesProcessor media_manifest fast-path enabled", zap.Int("pinned_entries", pins.EntryCount()))
		}
		if root != nil && root.AI != nil && root.AI.ScriptGen != nil {
			if ollamaClient := root.AI.ScriptGen.GetClient(); ollamaClient != nil {
				imageStage.WithSemanticScorer(ollamaadapters.NewOllamaPersonImageScorer(ollamaClient))
				log.Info("InternetImagesProcessor wired with GPU-capable Ollama person-image verification", zap.String("model", ollamaClient.Model()))
			}
		}
		if !ppReg.Register(imageStage) {
			return fmt.Errorf("register internet_images processor: composition bug")
		}
		log.Info("InternetImagesProcessor wired through canonical ImageSearchResolver")
	} else {
		log.Warn("InternetImagesProcessor: ImageSearchResolver not available; postprocessor not registered")
	}

	// ── Visual planning ──────────────────────────────────────────────
	// The processor owns no provider or database implementation: the
	// composition root supplies the canonical MediaMemory resolver, which
	// fans out through the shared SearchFanOut exactly once per plan.
	// Gemma is used as a ranking strategy to choose among the closed
	// candidate list returned by the resolver; on LLM failure the
	// adapter deterministically falls back to the top-scoring candidate.
	var visualPlanner adapters.VisualCandidatePlanner
	if searchFanOut != nil && root.DB != nil {
		resolver, err := WireMediaMemoryResolver(searchFanOut, root.DB.DB, log)
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
