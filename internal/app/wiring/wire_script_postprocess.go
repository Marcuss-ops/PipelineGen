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

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"

	"go.uber.org/zap"
)

// assetServiceLookupAdapter wraps asset.Service to satisfy the
// drive.AssetStoreLookup interface needed by LocationVerifier
// for deep Drive+SQLite cross-reference.
type assetServiceLookupAdapter struct {
	svc *asset.Service
}

func (a *assetServiceLookupAdapter) GetAsset(
	ctx context.Context, assetID string,
) (*asset.Details, error) {
	return a.svc.Get(ctx, assetID)
}

// registerScriptPostProcessors initialises and registers every
// canonical postprocessor on the supplied registry. Each registration
// is gated on its required infrastructure dependency (DocClient for
// document, ImageService for images, VoiceoverService for voiceover,
// QdrantSearcher + OllamaClient for clip_search). DocumentProcessor
// is always registered with a typed unavailable publisher when Drive
// is absent, so docs-enabled requests fail closed at runtime rather
// than being silently skipped.
//
// SCRIPTCONTRACT-2026-07-08 PR-1 canonical order (godlike/06 SSOT,
// extended PR-TRANSLATE-SCRIPT-SPEC FP2 2026-08-08 with TranslationProcessor):
//
//	Persistence → Document → Image → Voiceover → Entities → Metadata →
//	Translation → ClipBindings → VisualPlanning → ClipSearch
//
// Persistence is FIRST so no Drive-write side effect runs before the
// local SQLite row is locked. Pre-PR-1 order was Document →
// Persistence → ... which caused RETRY_WAIT 90% on a Drive-write-
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
	root *wiring.ComposeRoot,
	artlistWiring *wiring.ArtlistWiring,
	cfg *config.Config,
	log *zap.Logger,
	scriptsRepoAdapter adapters.ScriptRepository,
	metaModel string,
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
		if !ppReg.Register(adapters.NewPersistenceProcessor(scriptsRepoAdapter, log)) {
			return fmt.Errorf("register persistence processor: composition bug or duplicate name")
		}
	}

	// Documents are opt-in per request. Always register the processor so
	// docs-enabled plans fail closed when Drive is unavailable instead of
	// silently skipping document output at composition time.
	var docClient drive.DocClient
	if root != nil && root.Drive != nil {
		docClient = root.Drive.DocClient
	}
	docPublisher := newDriveDocumentPublisherAdapter(docClient)
	docService := usecase.NewDocumentsService(docPublisher, log, cfg.Drive.DocumentsFolder())
	if !ppReg.Register(adapters.NewDocumentsProcessor(docService)) {
		return fmt.Errorf("register document processor: composition bug or duplicate name")
	}
	log.Info("DocumentProcessor (Google Docs publishing) successfully registered")

	// Inline Image generation processor (temporarily restored).
	if root.Domains != nil && root.Domains.ImageService != nil {
		imgGenSvc := &imageGenSvcAdapter{svc: root.Domains.ImageService}
		if !ppReg.Register(adapters.NewImageProcessor(imgGenSvc, log)) {
			return fmt.Errorf("register image processor: composition bug or duplicate name")
		}
		log.Info("ImageProcessor (inline scene images) successfully registered")
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
		voProc := adapters.NewVoiceoverProcessor(root.Domains.VoiceoverProcessItem, log)
		voiceMap := make(map[string]string)
		if registry, registryErr := wiring.BuildLanguageRegistry(wiring.ActiveMultilingualConfig(cfg)); registryErr == nil {
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
	// bindings consumed by both the Google Doc builder (via
	// DocumentProcessor) AND the JSON response writer (via
	// result.Output.SpecScene.Scenes). BestEffort policy.
	if !ppReg.Register(adapters.NewClipBindingsProcessor(log)) {
		return fmt.Errorf("register clip_bindings processor: composition bug")
	}
	if !ppReg.Register(adapters.NewStockBindingsProcessor()) {
		return fmt.Errorf("register stock_bindings processor: composition bug")
	}
	if !ppReg.Register(adapters.NewNarrationSanitizer()) {
		return fmt.Errorf("register narration_sanitizer processor: composition bug")
	}

	// AssetLocationReconciliationProcessor verifies every drive_link
	// in the SpecScene bindings before the document is published.
	// BestEffort policy: transport errors become warnings, link
	// integrity is best-effort.
	//
	// When SQLite (asset.Service) is available, the processor uses
	// the deep LocationVerifier which cross-references Drive API
	// results against the asset store to detect ORPHAN_DRIVE_FILE,
	// BROKEN_ASSET_LOCATION, and DUPLICATE states.
	//
	// When SQLite is unavailable, it falls back to the lighter
	// AssetLocationResolverAdapter (Drive-only: MISSING, TRASHED,
	// INACCESSIBLE, VERIFIED, UPDATED).
	if root != nil && root.Drive != nil && root.Drive.Reader != nil {
		var verifier scriptpkg.AssetLocationVerifier
		if root.Repos != nil && root.Repos.Assets != nil {
			adapter := &assetServiceLookupAdapter{svc: root.Repos.Assets}
			verifier = drive.NewLocationVerifier(root.Drive.Reader, adapter)
			log.Info("AssetLocationReconciliationProcessor (BestEffort, deep: Drive+SQLite) successfully registered")
		} else {
			verifier = drive.NewAssetLocationResolverAdapter(root.Drive.Reader)
			log.Info("AssetLocationReconciliationProcessor (BestEffort, shallow: Drive-only) successfully registered")
		}
		if !ppReg.Register(adapters.NewAssetLocationReconciliationProcessor(verifier)) {
			return fmt.Errorf("register asset_location_reconciliation processor: composition bug or duplicate name")
		}
	}

	// VidRush provider registry + cache are built once by the orchestrator
	// (wireScriptFlow) and shared here with both the AI-backed processors and
	// the durable incremental runtime. Providers remain unavailable when their
	// concrete dependencies are absent; no empty-success adapter is registered.

	// AI-backed processors (entities, metadata, translation,
	// visual_planning, clip_search) — see wire_script_postprocess_ai.go.
	if err := registerAIBackedProcessors(ppReg, root, artlistWiring, vidRushProviders, vidRushCache, cfg, log); err != nil {
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
