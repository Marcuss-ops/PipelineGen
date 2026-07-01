// Package app — voiceover service bundle construction (Wave 15 PR4d-final).
//
// Extracted from compose_media.go per architecture/current.yaml::Wave 15
// pending #1 ("Migrate the 3 shared helpers... into modules/voiceover.go,
// modules/content.go, modules/images.go"). The "modules/" path in the
// pending item is a forward reference; the canonical current target for
// per-capability helpers is `internal/app/build_bundles_<capability>.go`,
// matching the buildIngestService / buildHealthService / buildSyncTargets
// pattern already established by build_bundles_core.go / build_bundles_process.go
// / build_bundles_domain.go.
//
// Private-helper convention (lowercase `build*`) — these helpers are NOT
// standalone composable bundles; they are internal to BuildDomainBundle
// (build_bundles_domain.go) which aggregates their output into
// ComposeRoot.Domains.
package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
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
// PR-VO-B1 (June 2026): voiceover.DriveUploaderPort is now the
// narrow Drive surface voiceover consumes (DeleteFile only, used by
// processLanguage's post-commit cleanup goroutine). The production
// concrete *drive.Uploader is wrapped by newVoiceoverDriveAdapter
// in adapters_voiceover_drive.go because the canonical layering
// rule forbids infrastructure/drive from importing
// application/voiceover. The port is structurally satisfied by
// the wrapper via Go's implicit-interface rules, with a nil-safe
// factory (returns nil when up is unwired).
func buildVoiceoverService(
	ctx context.Context,
	cfg *config.Config,
	dbs *databases,
	log *zap.Logger,
	driveUploader *drive.Uploader,
	assetIndexService *assetindex.Service,
	clipIndexerService *clipindexer.Service, // PR-VO-A3: no longer injects clipIndexFn into voiceover.Service; retained on the signature only because other voiceover paths still reach the indexer directly.
	destResolver asset.Resolver,
	metaWriter *semantic.MetadataWriter,
	scriptGen *ollama.Generator,
	outboxDispatcher *outbox.Dispatcher,
) (*voiceover.Service, *assets.VoiceoversRepository, voiceover.VoiceoverItemExecutor) {

	voDir := cfg.Storage.VoiceoversPath()
	voRepo := assets.NewVoiceoversRepository(dbs.main.DB)

	// P1-2 (June 2026): persistence.Repository adapter — wraps the
	// production *sqassets.VoiceoversRepository + *sql.DB so the
	// voiceover.Service can thread the PR-VO-A2 atomic swap tx
	// through a canonical port instead of holding a *sql.DB field
	// (the previous field was removed in P1-2 commit 1).
	voRepoAdapter := newUseCaseRepoAdapter(voRepo, dbs.main.DB)

	// Voiceover registry adapter — wraps the SQLite vo repo as a
	// lifecycle.Registry so NewLifecycleFromDeps accepts it.
	voRegistryAdapter := voiceover.NewVoiceoverRegistryAdapter(voRepo)

	voLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    voRegistryAdapter,
		DriveAdmin:  driveUploader,
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

	// Build translator closure from scriptGen (used by promo generation
	// to translate voiceover text into target language). Graceful
	// degradation: if scriptGen is nil, return input unchanged so promo
	// generation can still proceed.
	translator := func(ctx context.Context, text, targetLanguage string) (string, error) {
		if scriptGen == nil {
			return text, nil
		}
		return scriptGen.TranslateText(ctx, text, targetLanguage)
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

	// P1-2 (June 2026): the application layer no longer constructs
	// the production *audioasset.Processor. Construction moves UP to
	// the composition root (this file) so the voiceover package can
	// stay free of any internal/infrastructure/* import. The
	// processor is wrapped by newUseCaseTTSAdapter so the
	// voiceover.Service only sees the canonical TTSProvider port.
	if cfg.Paths.PythonScriptsDir == "" {
		log.Warn("voiceover: cfg.Paths.PythonScriptsDir is empty; audioasset.NewProcessor will be called with an empty string (TTS invocation will fail at runtime)")
	}
	audioProcessor := audioasset.NewProcessor(cfg.Paths.PythonScriptsDir, log)
	ttsProvider := newUseCaseTTSAdapter(audioProcessor)// (June 2026 BLOC5.4 cutover — Step 8/12): the canonical per-item
// voiceover pipeline ProcessVoiceoverItemUseCase is constructed on
// top of the same adapter surface the legacy service consumes.
// All 7 ports are mandatory (panic on nil per AGENTS.md WireUp
// pattern); the composition root owns this construction so any
// missing wire-up fails fast at boot rather than mid-job.	// Step 8/12 closure (June 2026 — BLOC5.4): wire the canonical
	// per-item use case ProcessVoiceoverItemUseCase on top of the
	// 7-port typed seam (Pattern 0 — AGENTS.md). The 7 mandatory deps
	// mirror the surface the legacy Service consumes (TTSProvider for
	// Stage 1, DestinationResolver for Stage 0, AudioPostProcessor for
	// Stage 2, AssetLifecycle for Stage 3 Drive upload, VoiceoverRepository
	// for Stage 4 atomic-swap tx, TransactionalOutbox for Stage 4
	// matching outbox enqueue, FilenameBuilder for forward BACKFILL
	// surface stability). NewProcessVoiceoverItemUseCase panics on nil
	// per fail-fast WireUp pattern so a partial wire-up surfaces at
	// boot, not at handler dispatch.
	//
	// Nil-tolerant construction (mirrors the in-file outboxDispatcher
	// block above: ~line 110). When destResolver is NOT supplied (typical
	// of `internal/app/*_test.go` stub-bootstrap helpers, which exercise
	// the composition root without standing up the full asset.Resolver
	// chain), the use case construction is SKIPPED and processItemUseCase
	// returns nil; the canonical fail-fast contracts of
	// newUseCaseDestResolverAdapter + voiceover.NewProcessVoiceoverItemUseCase
	// are preserved (no adapter-side nil-tolerance). The handler
	// (voiceoverjobs.NewGenerateItemJobHandler) is bound to the typed
	// VoiceoverItemExecutor port — a nil use case surfaces as a
	// Handler-Register-time error, not a mid-dispatch panic. This is
	// the minimum-scope fix for Step 8/12 wiring: the production
	// composition root path supplies a real destResolver so the warning
	// never fires in operator environments.
	var processItemUseCase voiceover.VoiceoverItemExecutor
	if destResolver != nil {
		// Adapter: DestinationResolver port — wraps destResolver
		// (asset.Resolver). Forward Group + StyleGroup to the resolver;
		// mirror StyleGroup verbatim on the returned ResolvedDestination.
		destResolverAdapter := newUseCaseDestResolverAdapter(destResolver)

		// Adapter (E1 cutover): VoiceoverPublisher port — wraps
		// driveUploader.Admin() directly. The legacy AssetLifecycle
		// adapter (useCaseLifecycleAdapter wrapping voLifecycle) is
		// retired; Publisher does NOT write to SQLite, does NOT run a
		// dedupe gate, does NOT touch media_assets (finalizeStage
		// owns the per-item tx).
		publisherAdapter := newUseCasePublisherAdapter(driveUploader)

		// Adapter: AudioPostProcessor port — silence-removal bridge
		// built on the canonical ffmpeg.RemoveSilence closure. Nil-safe
		// at the use case boundary (only invoked when RemoveSilence == true).
		audioAdapter := newUseCaseAudioAdapter(log)

		// Concrete: FilenameBuilder port (single shared stateless instance).
		filenameBuilder := voiceover.NewDefaultFilenameBuilder()

		// Cast: TransactionalOutbox is a type-alias for TxOutboxEnqueuer;
		// the production *outbox.Dispatcher satisfies the structural
		// contract without a wrapper. See process_voiceover_item.go.
		var txOutbox voiceover.TransactionalOutbox
		if outboxDispatcher != nil {
			txOutbox = outboxDispatcher
		}

		// The use case satisfies voiceover.VoiceoverItemExecutor
		// structurally — compile-time assertion in process_voiceover_item.go
		// pins the conformance.

		// P0.2 nil-destination fallback (July 2026): wire the
		// DefaultFolderResolver from the configured Voiceover folder
		// so a nil-Destination request resolves to the operator-
		// configured default rather than failing with missing_
		// destination. Nil-safe: newUseCaseDefaultFolderResolverAdapter
		// returns ("", "", false) when no folder is configured.
		defaultFolderResolver := newUseCaseDefaultFolderResolverAdapter(
			cfg.Drive.VoiceoverFolder(),
			voDir,
		)

		processItemUseCase = voiceover.NewProcessVoiceoverItemUseCase(voiceover.ProcessVoiceoverItemDeps{
			TTSProvider:           ttsProvider,
			DestinationResolver:   destResolverAdapter,
			AudioPostProcessor:    audioAdapter,
			Publisher:             publisherAdapter,
			VoiceoverRepository:   voRepoAdapter,
			TransactionalOutbox:   txOutbox,
			FilenameBuilder:       filenameBuilder,
			DefaultFolderResolver: defaultFolderResolver,
			Logger:                log,
		})
		log.Info("voiceover.processVoiceoverItemUseCase wired (Step 8/12 — child pipeline for voiceover.generate_item jobs)")
	} else {
		log.Warn("voiceover: skipping ProcessVoiceoverItemUseCase wire-up (Step 8/12); destResolver is nil — VoiceoverItemExecutor port returns nil; handler registration will fail-fast (NewGenerateItemJobHandler panic-on-nil useCase) at composition time in production paths that exercise the typed port")
	}

	voService := voiceover.NewService(voiceover.VoiceoverDeps{
		Core:        voiceover.VoiceoverCoreDeps{Cfg: cfg, Log: log, OutputDir: voDir},
		Persistence: voiceover.VoiceoverPersistenceDeps{Repo: voRepoAdapter},
		Generation: voiceover.VoiceoverGenerationDeps{
			TTSProvider:    ttsProvider,
			SemanticTagger: semanticTagger,
		},
		Integration: voiceover.VoiceoverIntegrationDeps{
			DriveUploader:     newVoiceoverDriveAdapter(driveUploader),
			LifecycleService:  voLifecycle,
			AssetDestResolver: destResolver,
			OutboxEnqueuer:    outboxEnqueuer,
			Translator:        translator,
		},
	})
	// pylint: disable=unused
	_ = clipIndexerService // retained on the signature for future use; IndexClip is now reached only via the outbox dispatcher → IndexingHandler → clipIndexerService.IndexClip instead.
	log.Info("Voiceover service initialized", zap.String("python_scripts_dir", cfg.Paths.PythonScriptsDir))

	return voService, voRepo, processItemUseCase
}
