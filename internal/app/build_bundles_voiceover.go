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

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
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
func buildVoiceoverPipeline(
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
	outboxDispatcher *outbox.Dispatcher,
	committer assetspersistence.AssetCommitter,
	mediaConfig mediaexec.ExecutionConfig,
) (*assets.VoiceoversRepository, voiceover.VoiceoverItemExecutor, *audioasset.Processor, error) {
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
	// PR-VO-A3: outbox enqueuer. Production wiring supplies
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
	// per finalizer_invariants_test.go:390. The canonical AssetCommitter
	// (VOICEOVER-ASSETCOMMITTER-CUTOVER) makes Step 4+5 a single CommitTx;
	// the finalizer falls back to the legacy projection writer only for
	// the empty-FileHash edge case.
	finalizer := voiceover.NewVoiceoverFinalizer(
		voRepoAdapter,     // VoiceoverRepository
		outboxEnqueuer,    // TxOutboxEnqueuer (nil-safe in finalizer)
		projectionAdapter, // LifecycleProjectionUpserter
		committer,         // canonical AssetCommitter (Step 4+5 via CommitTx)
		log,               // *zap.Logger
	)

	// TTS chain — raw processor → use-case adapter → retryable → rate-limited.
	// Extracted to build_voiceover_tts.go (shared by the legacy batch
	// service and the canonical per-item use case).
	audioProcessor, ttsProvider := buildVoiceoverTTSProvider(cfg, log, mediaConfig)

	// Azione #1 (July 2026): construct the shared per-item pipeline
	// runner. The legacy batch path (process.go::processLanguage) now
	// delegates to ProcessSegmentUseCase.Execute instead of calling
	// synthesizeStage/destinationStage/finalizeStage inline.
	//
	// FASE 8: Publisher wrapped with rate-limiting + retry.
	voPublisher := newRateLimitedPublisher(newUseCasePublisherAdapter(publisher, log), cfg.Voiceover, log)
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
	// built on the canonical media execution adapter. Nil-safe
	// at the use case boundary (only invoked when RemoveSilence == true).
	audioAdapter := newUseCaseAudioAdapter(log, rustexec.NewConfiguredVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, mediaConfig.Policy, mediaConfig.Profile, log))

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
	// PR-CATALOG-MULTILINGUA step 3 (July 2026): build the canonical
	// per-language capability surface used by the voiceover pipeline to
	// resolve EdgeTTSVoice identifiers. A nil registry falls through
	// to the bridge's emergency fallback (nil-safe in process.go).
	// P0-#3 (July 2026): wire the canonical per-item voiceover use
	// case into the legacy Service so GeneratePromo can route
	// through it. processItemUseCase was already constructed above
	// (Step 8/12 — BLOC5.4 cutover); the field is now consumed by
	// the Service's promo path via the promoVoiceoverAdapter
	// (voiceover/promo.go). When the wire-up is skipped (the
	// destResolver==nil branch above), processItemUseCase is nil
	// and Service.GeneratePromo surfaces a typed error — fail-closed
	// per godlike/07 NO-FAKE-AVAILABILITY.
	// pylint: disable=unused
	_ = clipIndexerService // retained on the signature for future use; IndexClip is now reached only via the outbox dispatcher → IndexingHandler → clipIndexerService.IndexClip instead.
	log.Info("Voiceover canonical pipeline initialized", zap.String("python_scripts_dir", cfg.Paths.PythonScriptsDir))

	return voRepo, processItemUseCase, audioProcessor, nil
}
