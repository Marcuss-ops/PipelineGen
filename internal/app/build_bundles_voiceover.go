// Package app — voiceover service bundle construction (Wave 15 PR4d-final).
//
// Extracted from compose_media.go per architecture/current.yaml::Wave 15
// pending #1 ("Migrate the 3 shared helpers... into modules/voiceover.go,
// modules/content.go, modules/images.go"). The "modules/" path in the
// pending item is a forward reference; the canonical current target for
// per-capability helpers is `internal/app/build_bundles_<capability>.go`,
// matching the buildIngestService / buildHealthService / wiring.BuildSyncTargets
// pattern already established by build_bundles_core.go / build_bundles_process.go
// / build_bundles_domain.go.
//
// Private-helper convention (lowercase `build*`) — these helpers are NOT
// standalone composable bundles; they are internal to BuildDomainBundle
// (build_bundles_domain.go) which aggregates their output into
// wiring.ComposeRoot.Domains.
//
// File layout (domain split, July 2026):
//   - this file: buildVoiceoverService orchestrator
//   - build_voiceover_tts.go: TTS provider chain construction
//   - build_voiceover_destinations.go: destination resolver adapters
//     (+ nopDestinationResolver test-bootstrap fallback)
//   - build_voiceover_jobs.go: wireVoiceoverJobBindings
//   - build_voiceover_validators.go: appendVoiceoverCriticalValidators
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildVoiceoverService sets up the voiceover service and its repository.
//
// PR4-H (June 2026): SetSemanticTagger / SetTranslator / SetClipIndexer
// setters have been removed from voiceover.NewService — the callbacks
// required for promo generation, translation, and post-enrichment
// indexing are now passed as constructor arguments.
//
// PR-VO-A3 (June 2026): the legacy ClipIndexFunc callback is gone —
// the indexing handoff now flows through the canonical outbox path.
// The voiceover swap (swapVoiceoverRow) enqueues
// `asset.index.requested` inside its SQLite transaction via the
// TxOutboxEnqueuer port, so the metadata UPSERT and the indexing
// event INSERT commit atomically. The concrete adapter is the
// production outbox.Dispatcher (passed as outboxDispatcher below),
// structurally satisfying voiceover.TxOutboxEnqueuer via Go's
// implicit-interface rules.
//
// Azione #9 follow-up (July 2026): DriveUploaderPort interface removed
// from ports.go; voiceoverDriveAdapter struct also removed from
// adapters_voiceover_publisher.go. Post-commit Drive cleanup now flows
// directly through drive.Admin → jobsoutbox.VoiceoverCleanupDriver
// (structural conformance, no wrapper needed).
func buildVoiceoverService(
	ctx context.Context,
	cfg *config.Config,
	dbs *wiring.Databases,
	log *zap.Logger,

	driveUploader *drive.Uploader,
	publisher delivery.Publisher,
	assetIndexService *assetindex.Service,
	clipIndexerService *clipindexer.Service, // PR-VO-A3: no longer injects clipIndexFn into voiceover.Service; retained on the signature only because other voiceover paths still reach the indexer directly.
	destResolver asset.Resolver,
	metaWriter semantic.MetadataWriterPort,
	translationPort translation.TranslationPort, // Fase 9 step 4 CUTOVER: *translation.OllamaTranslator satisfies this port; the bare *ollama.Generator.TranslateText direct-call closure is RETIRED.
	outboxDispatcher *outbox.Dispatcher,
) (*voiceover.Service, *assets.VoiceoversRepository, voiceover.VoiceoverItemExecutor, *audioasset.Processor, error) {

	// FASE 9 (July 2026): fail-closed gate — when cfg.Translation.Required
	// is true, the translation port MUST be wired. A nil port is a
	// composition-time misconfiguration; panic with an actionable message
	// so the operator sees the boot failure and fixes the wiring rather
	// than discovering the silent fallback at the first promo translation
	// request (godlike/07 NO-FAKE-AVAILABILITY: never silently degrade
	// a required production dependency).
	if cfg.Translation.Required && translationPort == nil {
		panic("voiceover: cfg.Translation.Required=true but translationPort is nil — " +
			"the voiceover pipeline requires a translation.TranslationPort (e.g. *translation.OllamaTranslator) " +
			"for promo generation. Set cfg.Translation.Required=false for dev mode, or wire the port " +
			"via build_bundles_ai.go → BuildDomainBundle → buildVoiceoverService.")
	}

	voDir := cfg.Storage.VoiceoversPath()
	voRepo := assets.NewVoiceoversRepository(dbs.DualPool.Writer)

	// P1-2 (June 2026): persistence.Repository adapter — wraps the
	// production *sqassets.VoiceoversRepository + *sql.DB so the
	// voiceover.Service can thread the PR-VO-A2 atomic swap tx
	// through a canonical port instead of holding a *sql.DB field
	// (the previous field was removed in P1-2 commit 1).
	voRepoAdapter := newUseCaseRepoAdapter(voRepo, dbs.DualPool.Writer)

	// Voiceover registry adapter — wraps the SQLite vo repo as a
	// lifecycle.Registry so NewLifecycleFromDeps accepts it.
	voRegistryAdapter := voiceover.NewVoiceoverRegistryAdapter(voRepo)

	voLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{
		Registry:    voRegistryAdapter,
		Publisher:   publisher,
		DriveReader: driveUploader,
		AssetIndex:  assetIndexService,
	}, log)

	// Build semantic-tagger closure from metaWriter (used by promo
	// generation to enrich voiceover assets with search_text/tags).
	semanticTagger := func(ctx context.Context, prompt, style, mediaType, generator string) (*voiceover.SemanticTaggerResult, error) {
		if metaWriter == nil {
			return nil, fmt.Errorf("voiceover: metaWriter not wired (cannot enrich voiceover semantic metadata)")
		}
		payload, _, err := metaWriter.GeneratePayload(ctx, semantic.WriteRequest{
			AssetID:   "",
			AssetType: "voiceover",
			MediaType: mediaType,
			Source:    "voiceover",
			Generator: generator,
			Style:     style,
			Prompt:    prompt,
		})
		if err != nil {
			return nil, err
		}
		return &voiceover.SemanticTaggerResult{
			SearchText: payload.SearchText,
			Tags:       payload.Tags,
			Subjects:   payload.Subjects,
			Mood:       payload.Mood,
		}, nil
	}

	// P0.4 Fase 3a (July 2026): construct the unified VoiceoverFinalizer
	// once and inject it into BOTH the per-item use case (child pipeline)
	// and the legacy Service (batch pipeline). The finalizer replaces the
	// two divergent finalization paths with a single 6-step atomic commit
	// sequence: dedupe → delete → insert → media_assets projection →
	// index outbox → cleanup outbox.
	//
	// Dependencies:
	//   - voRepoAdapter:   persistence.Repository (BeginTx, InsertTx,
	//                       DeleteByIDTx, CountByDriveFileIDTx)
	//   - outboxEnqueuer:  TxOutboxEnqueuer (EnqueueIndexEvent,
	//                       EnqueueCleanupEvent) — nil-safe
	//   - voLifecycle:     *lifecycle.Service → LifecycleProjectionUpserter
	//                       via voiceoverProjectionAdapter
	//   - log:             *zap.Logger
	// FASE 8 (July 2026): wrap the translation port with bounded
	// concurrency (Ollama semaphore) and per-call timeout BEFORE
	// the closure captures it. The closure's nil-guard still works
	// when translationPort is nil (rate-limited wrapper is skipped).
	if translationPort != nil {
		translationPort = newRateLimitedTranslator(translationPort, cfg.Voiceover, log)
	}

	// Build translator closure from translationPort (canonical Fase 9
	// step 4 surface). The closure adapts the canonical 1-method port
	// (Translate(ctx, cmd TranslationCommand)) to the legacy 3-arg
	// TranslatorFunc signature voiceover.Service expects (ctx, text, lang)
	// → (string, error). The ContentKind=voiceover envelope tells the
	// provider this is a voiceover translation (preserves verbatim
	// formatting for TTS downstream). Graceful degradation: if
	// translationPort is nil, return input unchanged so promo generation
	// can still proceed (godlike/07 no-fake-availability: callers should
	// detect nil + wire, but the missing-wire failure mode is silent
	// fallback, not panic — same pattern as the pre-CUTOVER bare
	// scriptGen closure).
	translator := func(ctx context.Context, text, targetLanguage string) (string, error) {
		if translationPort == nil {
			return text, nil
		}
		res, err := translationPort.Translate(ctx, translation.TranslationCommand{
			TargetLang: targetLanguage,
			Text:       text,
		})
		if err != nil {
			observability.TranslationFailuresTotal.Inc()
			return text, err
		}
		return res.TranslatedText, nil
	}

	// PR-VO-A3: outbox enqueuer (idle if nil). The voiceover.Service
	// guards nil — see voiceover/ports.go. Production wiring supplies
	// the *outbox.Dispatcher; the field satisfies the TxOutboxEnqueuer
	// port structurally (no wrapper required).
	var outboxEnqueuer voiceover.TxOutboxEnqueuer
	if outboxDispatcher != nil {
		outboxEnqueuer = outboxDispatcher
		if clipIndexerService == nil || !clipIndexerService.IsEnabled() {
			log.Warn("voiceover service wired with outbox dispatcher but clipIndexer disabled — asset.index.requested events will be enqueued but no consumer-side indexing will execute")
		}
	} else {
		log.Warn("voiceover service wired WITHOUT outbox dispatcher — indexing will be SKIPPED (no asset.index.requested events emitted)")
	}

	projectionAdapter := newVoiceoverProjectionAdapter(voLifecycle)
	// NewVoiceoverFinalizer sig is (repo, outbox, lifecycle, committer, logger)
	// per finalizer_invariants_test.go:390. committer=nil preserves the
	// Step 4+5 legacy branch (clip_atomic_writer.go:132 wires canonical).
	finalizer := voiceover.NewVoiceoverFinalizer(
		voRepoAdapter,     // VoiceoverRepository
		outboxEnqueuer,    // TxOutboxEnqueuer (nil-safe in finalizer)
		projectionAdapter, // LifecycleProjectionUpserter
		nil,               // committer (nil preserves legacy pre-Cutover branch)
		log,               // *zap.Logger
	)

	// TTS chain — raw processor → use-case adapter → retryable → rate-limited.
	// Extracted to build_voiceover_tts.go (shared by the legacy batch
	// service and the canonical per-item use case).
	audioProcessor, ttsProvider := buildVoiceoverTTSProvider(cfg, log)

	// Azione #1 (July 2026): construct the shared per-item pipeline
	// runner. The legacy batch path (process.go::processLanguage) now
	// delegates to ProcessSegmentUseCase.Execute instead of calling
	// synthesizeStage/destinationStage/finalizeStage inline.
	//
	// FASE 8: Publisher wrapped with rate-limiting + retry.
	voPublisher := newRateLimitedPublisher(newUseCasePublisherAdapter(publisher, log), cfg.Voiceover, log)
	processSeg := voiceover.NewProcessSegmentUseCase(voiceover.ProcessSegmentDeps{
		TTSProvider:         ttsProvider,
		AudioPostProcessor:  newUseCaseAudioAdapter(log),
		Publisher:           voPublisher,
		VoiceoverRepository: voRepoAdapter,
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    outboxEnqueuer, // FASE 4 (July 2026): orphan-cleanup path active in production
		SemanticTagger:      semanticTagger,
		Logger:              log,
	})

	// (June 2026 BLOC5.4 cutover — Step 8/12): the canonical per-item
	// voiceover pipeline ProcessVoiceoverItemUseCase is constructed on
	// top of the same adapter surface the legacy service consumes.
	// All 7 ports are mandatory (panic on nil per AGENTS.md WireUp
	// pattern); the composition root owns this construction so any
	// missing wire-up fails fast at boot rather than mid-job.	// Step 8/12 closure (June 2026 — BLOC5.4): wire the canonical
	// per-item use case ProcessVoiceoverItemUseCase on top of the
	// typed seam (Pattern 0 — AGENTS.md). The mandatory deps
	// mirror the surface the legacy Service consumes.
	// NewProcessVoiceoverItemUseCase panics on nil per fail-fast
	// WireUp pattern so a partial wire-up surfaces at boot, not at
	// handler dispatch.
	//
	// Azione #6 (July 2026): TransactionalOutbox and FilenameBuilder
	// removed from ProcessVoiceoverItemDeps. TransactionalOutbox was
	// a type-alias never used by Execute (finalizer owns the outbox,
	// PR-VO-B3). FilenameBuilder was nil-safe per Azione #5.
	//
	//
	// Nil-tolerant construction (mirrors the in-file outboxDispatcher
	// block above: ~line 110). When destResolver is NOT supplied (typical
	// of `internal/app/*_test.go` stub-bootstrap helpers, which exercise
	// the composition root without standing up the full asset.Resolver
	// chain), a nil-tolerant nopDestinationResolver is wired so the
	// ProcessVoiceoverItemUseCase constructor's
	// `if deps.DestinationResolver == nil { panic }` check passes.
	// The use case's Execute tolerates a resolver that returns
	// (nil, nil) via the canonical "missing_folder_id" short-circuit
	// in ResolveDestinationWithFallback — the production semantics
	// are preserved (a request without an explicit Destination always
	// fails closed, regardless of whether the resolver is real or
	// nop).
	//
	// P0-#3 (July 2026): processItemUseCase is now ALWAYS constructed
	// (not gated on destResolver != nil) because the NewService
	// composition-time fail-fast panics when ProcessItem is nil. The
	// use case itself tolerates nil DestinationResolver + nil
	// DefaultFolderResolver via the canonical short-circuit path.
	// Destination resolver adapters — extracted to
	// build_voiceover_destinations.go (includes the nil-tolerant
	// nopDestinationResolver fallback for stub-bootstrap helpers).
	destResolverAdapter, defaultFolderResolver := buildVoiceoverDestResolvers(destResolver, cfg, voDir, log)

	// Adapter (E1 cutover): VoiceoverPublisher port — wraps
	// driveUploader.Admin() directly. The legacy AssetLifecycle
	// adapter (useCaseLifecycleAdapter wrapping voLifecycle) is
	// retired; Publisher does NOT write to SQLite, does NOT run a
	// dedupe gate, does NOT touch media_assets (finalizeStage
	// owns the per-item tx).
	// FASE 8: wrapped with rate-limiting + retry (same instance
	// as the batch path's voPublisher above — shared semaphore).
	publisherAdapter := voPublisher

	// Adapter: AudioPostProcessor port — silence-removal bridge
	// built on the canonical ffmpeg.RemoveSilence closure. Nil-safe
	// at the use case boundary (only invoked when RemoveSilence == true).
	audioAdapter := newUseCaseAudioAdapter(log)

	// The use case satisfies voiceover.VoiceoverItemExecutor
	// structurally — compile-time assertion in process_voiceover_item.go
	// pins the conformance.
	processItemUseCase := voiceover.NewProcessVoiceoverItemUseCase(voiceover.ProcessVoiceoverItemDeps{
		Pipeline: voiceover.ProcessVoiceoverPipelineDeps{
			TTSProvider:         ttsProvider,
			DestinationResolver: destResolverAdapter,
			AudioPostProcessor:  audioAdapter,
			Publisher:           publisherAdapter,
			VoiceoverRepository: voRepoAdapter,
		},
		Recovery: voiceover.ProcessVoiceoverRecoveryDeps{
			DefaultFolderResolver: defaultFolderResolver,
		},
		Finalize: voiceover.ProcessVoiceoverFinalizeDeps{
			Finalizer: finalizer,
		},
		Output: voiceover.ProcessVoiceoverOutputDeps{
			OutputDir: voDir,
		},
		Logger: log,
	})
	log.Info("voiceover.processVoiceoverItemUseCase wired (Step 8/12 — child pipeline for voiceover.generate_item jobs)")

	// P0.4 Fase 4a (July 2026): wire the post-commit SQL verifier.
	// After the tx commits, finalizeStage calls Verify to confirm both
	// the voiceovers row and the media_assets projection are durably
	// present. The adapter uses dbs.DualPool.Writer for post-commit SELECTs.
	postCommitVerifier := newVoiceoverPostCommitVerifierAdapter(dbs.DualPool.Writer)

	// PR-CATALOG-MULTILINGUA step 3 (July 2026): build the canonical
	// per-language capability surface used by the voiceover pipeline to
	// resolve EdgeTTSVoice identifiers. A nil registry falls through
	// to the bridge's emergency fallback (nil-safe in process.go).
	languageRegistry, err := wiring.BuildLanguageRegistry(wiring.ActiveMultilingualConfig(cfg))
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("compose voiceover: language registry: %w", err)
	}

	// P0-#3 (July 2026): wire the canonical per-item voiceover use
	// case into the legacy Service so GeneratePromo can route
	// through it. processItemUseCase was already constructed above
	// (Step 8/12 — BLOC5.4 cutover); the field is now consumed by
	// the Service's promo path via the promoVoiceoverAdapter
	// (voiceover/promo.go). When the wire-up is skipped (the
	// destResolver==nil branch above), processItemUseCase is nil
	// and Service.GeneratePromo surfaces a typed error — fail-closed
	// per godlike/07 NO-FAKE-AVAILABILITY.
	voService := voiceover.NewService(voiceover.VoiceoverDeps{
		Core:        voiceover.VoiceoverCoreDeps{Cfg: cfg, Log: log, OutputDir: voDir},
		Persistence: voiceover.VoiceoverPersistenceDeps{Repo: voRepoAdapter},
		Generation: voiceover.VoiceoverGenerationDeps{
			TTSProvider:    ttsProvider,
			SemanticTagger: semanticTagger,
		},
		Integration: voiceover.VoiceoverIntegrationDeps{
			LifecycleService:  voLifecycle, // voiceover's lifecycle (NOT the retired PR-ARTLIST-LIFECYCLE artlist forward-pointer, 2026-07-04)
			AssetDestResolver: destResolver,
			OutboxEnqueuer:    outboxEnqueuer,
			Translator:        translator,
			LanguageRegistry:  languageRegistry,
		},
		Execution: voiceover.VoiceoverExecutionDeps{
			Finalizer:          finalizer,
			PostCommitVerifier: postCommitVerifier,
			ProcessSegment:     processSeg,
			ProcessItem:        processItemUseCase, // P0-#3 (July 2026): the canonical per-item use case the promo path routes through
		},
	})
	// pylint: disable=unused
	_ = clipIndexerService // retained on the signature for future use; IndexClip is now reached only via the outbox dispatcher → IndexingHandler → clipIndexerService.IndexClip instead.
	log.Info("Voiceover service initialized", zap.String("python_scripts_dir", cfg.Paths.PythonScriptsDir))

	return voService, voRepo, processItemUseCase, audioProcessor, nil
}
