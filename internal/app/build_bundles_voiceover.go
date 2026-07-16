// Package app contains composition-root wiring for the voiceover capability.
package app

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildVoiceoverService constructs the voiceover service and its per-item
// execution pipeline.
func buildVoiceoverService(
	ctx context.Context,
	cfg *config.Config,
	dbs *databases,
	log *zap.Logger,
	driveUploader *drive.Uploader,
	publisher delivery.Publisher,
	assetIndexService *assetindex.Service,
	clipIndexerService *clipindexer.Service,
	destResolver asset.Resolver,
	metaWriter semantic.MetadataWriterPort,
	translationPort translation.TranslationPort,
	outboxDispatcher *outbox.Dispatcher,
) (*voiceover.Service, *assets.VoiceoversRepository, voiceover.VoiceoverItemExecutor, *audioasset.Processor) {
	_ = ctx
	if cfg.Translation.Required && translationPort == nil {
		panic("voiceover: cfg.Translation.Required=true but translationPort is nil — " +
			"the voiceover pipeline requires a translation.TranslationPort (e.g. *translation.OllamaTranslator) " +
			"for promo generation. Set cfg.Translation.Required=false for dev mode, or wire the port " +
			"via build_bundles_ai.go → BuildDomainBundle → buildVoiceoverService.")
	}

	voDir := cfg.Storage.VoiceoversPath()
	voRepo := assets.NewVoiceoversRepository(dbs.dualPool.Writer)
	voRepoAdapter := newUseCaseRepoAdapter(voRepo, dbs.dualPool.Writer)
	voRegistryAdapter := voiceover.NewVoiceoverRegistryAdapter(voRepo)
	voLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    voRegistryAdapter,
		Publisher:   publisher,
		DriveReader: driveUploader,
		AssetIndex:  assetIndexService,
	}, log)

	semanticTagger := buildVoiceoverSemanticTagger(metaWriter)
	if translationPort != nil {
		translationPort = newRateLimitedTranslator(translationPort, cfg.Voiceover, log)
	}
	translator := buildVoiceoverTranslator(translationPort)
	outboxEnqueuer := buildVoiceoverOutboxEnqueuer(outboxDispatcher, clipIndexerService, log)

	projectionAdapter := newVoiceoverProjectionAdapter(voLifecycle)
	finalizer := voiceover.NewVoiceoverFinalizer(
		voRepoAdapter,
		outboxEnqueuer,
		projectionAdapter,
		nil,
		log,
	)

	if cfg.Paths.PythonScriptsDir == "" {
		log.Warn("voiceover: cfg.Paths.PythonScriptsDir is empty; audioasset.NewProcessor will be called with an empty string (TTS invocation will fail at runtime)")
	}
	audioProcessor := audioasset.NewProcessor(cfg.Paths.PythonScriptsDir, log)
	var ttsProvider voiceover.TTSProvider = newUseCaseTTSAdapter(audioProcessor)
	ttsProvider = newRetryableTTSProvider(ttsProvider, cfg.Voiceover, log)
	ttsProvider = newRateLimitedTTSProvider(ttsProvider, cfg.Voiceover, log)

	voPublisher := newRateLimitedPublisher(newUseCasePublisherAdapter(publisher, log), cfg.Voiceover, log)
	processSeg := voiceover.NewProcessSegmentUseCase(voiceover.ProcessSegmentDeps{
		TTSProvider:         ttsProvider,
		AudioPostProcessor:  newUseCaseAudioAdapter(log),
		Publisher:           voPublisher,
		VoiceoverRepository: voRepoAdapter,
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    outboxEnqueuer,
		Logger:              log,
	})

	var destResolverAdapter voiceover.DestinationResolver
	var defaultFolderResolver voiceover.VoiceoverDefaultFolderResolver
	if destResolver != nil {
		destResolverAdapter = newUseCaseDestResolverAdapter(destResolver)
		defaultFolderResolver = newUseCaseDefaultFolderResolverAdapter(
			cfg.Drive.VoiceoverFolder(),
			voDir,
		)
	} else {
		destResolverAdapter = nopDestinationResolver{}
		log.Warn("voiceover: using nopDestinationResolver (no asset.Resolver wired); processItemUseCase will fail-closed with missing_folder_id for requests without explicit Destination (typical of internal/app/*_test.go stub-bootstrap helpers)")
	}

	processItemUseCase := voiceover.NewProcessVoiceoverItemUseCase(voiceover.ProcessVoiceoverItemDeps{
		TTSProvider:           ttsProvider,
		DestinationResolver:   destResolverAdapter,
		AudioPostProcessor:    newUseCaseAudioAdapter(log),
		Publisher:             voPublisher,
		VoiceoverRepository:   voRepoAdapter,
		DefaultFolderResolver: defaultFolderResolver,
		Finalizer:             finalizer,
		OutputDir:             voDir,
		Logger:                log,
	})
	log.Info("voiceover.processVoiceoverItemUseCase wired (Step 8/12 — child pipeline for voiceover.generate_item jobs)")

	postCommitVerifier := newVoiceoverPostCommitVerifierAdapter(dbs.dualPool.Writer)
	voService := voiceover.NewService(voiceover.VoiceoverDeps{
		Core:        voiceover.VoiceoverCoreDeps{Cfg: cfg, Log: log, OutputDir: voDir},
		Persistence: voiceover.VoiceoverPersistenceDeps{Repo: voRepoAdapter},
		Generation: voiceover.VoiceoverGenerationDeps{
			TTSProvider:    ttsProvider,
			SemanticTagger: semanticTagger,
		},
		Integration: voiceover.VoiceoverIntegrationDeps{
			LifecycleService:   voLifecycle,
			AssetDestResolver:  destResolver,
			OutboxEnqueuer:     outboxEnqueuer,
			Translator:         translator,
			Finalizer:          finalizer,
			PostCommitVerifier: postCommitVerifier,
			ProcessSegment:     processSeg,
			ProcessItem:        processItemUseCase,
		},
	})
	log.Info("Voiceover service initialized", zap.String("python_scripts_dir", cfg.Paths.PythonScriptsDir))
	return voService, voRepo, processItemUseCase, audioProcessor
}
