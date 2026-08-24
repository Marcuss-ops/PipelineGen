package voiceover

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/lifecycle"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
)

// SemanticTaggerFunc is a function that calls the Python semantic tagger.
// Defined as a callback to avoid circular imports with the images package.
type SemanticTaggerFunc func(ctx context.Context, prompt, style, mediaType, generator string) (*SemanticTaggerResult, error)

// SemanticTaggerResult mirrors the semantic tagger output for voiceover use.
type SemanticTaggerResult struct {
	SearchText string   `json:"search_text"`
	Tags       []string `json:"tags"`
	Subjects   []string `json:"subjects"`
	Mood       []string `json:"mood"`
}

// AudioProcessor interface removed in P1-2 (June 2026).
// The previous AudioProcessor interface wrapped *audioasset.Processor
// directly so processLanguage could call Generate(ctx, *AudioInput).
// P1-2 boundary split: TTSProvider becomes the canonical port for
// TTS synthesis (already declared in voiceover/ports.go since
// B-2 BACKFILL — June 2026). The concrete *audioasset.Processor is
// constructed in the composition root (internal/app/
// build_bundles_voiceover.go) + wrapped by newUseCaseTTSAdapter
// (internal/app/adapters_voiceover_use_case.go) so voiceover
// re-imports neither the concrete nor any *infrastructure/*.

type Service struct {
	cfg *config.Config
	// voiceoverRepo is the canonical persistence port for the
	// voiceovers SQLite row lifecycle (P1-2 boundary split,
	// June 2026). Replaces the *sql.DB field previously held by
	// Service and the raw tx.ExecContext calls in finalizeStage.
	// Production concrete: useCaseRepoAdapter (in
	// internal/app/adapters_voiceover_use_case.go) wrapping
	// *sqassets.VoiceoversRepository.
	voiceoverRepo persistence.Repository
	// ttsProvider is the canonical TTS synthesis port (B-2
	// BACKFILL + P1-2 boundary confirmation, June 2026). Replaces
	// the legacy `audioProcessor AudioProcessor` field that
	// wrapped *audioasset.Processor directly. Production
	// concrete: newUseCaseTTSAdapter (in
	// internal/app/adapters_voiceover_use_case.go) wrapping
	// *audioasset.Processor constructed in the composition root.
	ttsProvider TTSProvider
	outputDir   string
	log         *zap.Logger
	// processItem is the canonical per-item use case used by promo and
	// child-job flows. The positional Service entry points are retired;
	// all callers use command-driven use cases.
	assetDestResolver asset.Resolver
	semanticTagger    SemanticTaggerFunc
	// PR-VO-A3 (Outbox-based Qdrant indexing, June 2026): the
	// previous ClipIndexFunc callback (fire-and-forget goroutine)
	// is gone. The TxOutboxEnqueuer port replaces it — swapVoiceoverRow
	// now enqueues the canonical `asset.index.requested` envelope
	// INSIDE the SQLite transaction, so the metadata INSERT and the
	// indexing event INSERT commit atomically.
	//
	// May be nil at construction time; swapVoiceoverRow guards the nil
	// case so the optional behaviour degrades to "skip indexing"
	// (same pattern as the previous ClipIndexFunc callback).
	outboxEnqueuer TxOutboxEnqueuer
	translator     translation.TranslatorFunc // Deprecated: moved to translation pkg, kept for GeneratePromo compat
	// finalizer is the unified finalization port (P0.4 Fase 3a, July 2026).
	// Replaces the inline 6-step finalizeStage tx body with a single
	// Finalize(ctx, tx, cmd) call. The production concrete is wired in
	// build_bundles_voiceover.go from voRepoAdapter + outboxEnqueuer +
	// lifecycleProjectionAdapter + log. Nil-safe: finalizeStage guards
	// nil and returns a typed failure when unwired.
	finalizer VoiceoverFinalizer
	// postCommitVerifier is the optional post-commit SQL verification
	// port (P0.4 Fase 4a, July 2026). After the tx commits, finalizeStage
	// calls Verify(ctx, voiceoverID) to confirm both the voiceovers row
	// and the media_assets projection exist. Nil-safe: when unwired,
	// verification is skipped entirely.
	postCommitVerifier VoiceoverPostCommitVerifier
	// languageRegistry is the canonical per-language capability
	// surface used to resolve the EdgeTTSVoice for a language.
	// PR-CATALOG-MULTILINGUA step 3 (July 2026): the voiceover
	// pipeline queries the registry rather than hardcoding voices.
	languageRegistry asset.LanguageRegistry
	// processSeg is the shared per-item pipeline runner (Azione #1,
	// July 2026). The legacy batch path (process.go::processLanguage)
	// now delegates to ProcessSegmentUseCase.Execute instead of calling
	// synthesizeStage/destinationStage/finalizeStage inline. The same
	// runner is shared with the per-item use case path.
	processSeg *ProcessSegmentUseCase
	// processItem is the canonical per-item use case that the promo
	// workflow routes through (P0-#3, July 2026). Replaces the legacy
	// voiceoverGenBridge which called Service.GenerateWithDestination
	// (a positional API that masked failures via Result{OK:false}).
	//
	// The promo path (Service.GeneratePromo) constructs a
	// promoVoiceoverAdapter (see promo.go) that adapts the workflow's
	// domainvo.GenerateVoiceoverCommand to this use case's typed
	// GenerateVoiceoverItemCommand. Real failures surface as a typed Go
	// error wrapping ErrPromoVoiceoverGeneration — no more silent
	// Result{OK:false}.
	//
	// Mandatory (fail-fast): a nil processItem at construction time
	// would silently disable the promo path at runtime (the legacy
	// "soft no-op" pattern). P0-#3 promotes this to a hard
	// composition-time requirement so the missing wire-up is fixed
	// before deploy.
	processItem VoiceoverItemExecutor
}

// ALIAS REMOVED Fase 9 step 3: see architecture/deprecations.yaml#TRANSLATION-UNIFY (migration_phase: BACKFILL / status: contract-half)

// VoiceoverDeps groups constructor dependencies by real capability.
// Replaces the 11-param flat constructor with 4 grouped bundles.
type VoiceoverDeps struct {
	Core        VoiceoverCoreDeps
	Persistence VoiceoverPersistenceDeps
	Generation  VoiceoverGenerationDeps
	Integration VoiceoverIntegrationDeps
	Execution   VoiceoverExecutionDeps
}

// VoiceoverCoreDeps — config, logger, output directory.
type VoiceoverCoreDeps struct {
	Cfg       *config.Config
	Log       *zap.Logger
	OutputDir string
}

// VoiceoverPersistenceDeps — voiceover repository port.
type VoiceoverPersistenceDeps struct {
	Repo persistence.Repository
}

// VoiceoverGenerationDeps — TTS provider + semantic tagger.
//
// The canonical per-item use case is injected separately and is the
// only voiceover generation surface used by promo and child jobs.
type VoiceoverGenerationDeps struct {
	TTSProvider    TTSProvider
	SemanticTagger SemanticTaggerFunc
}

// VoiceoverIntegrationDeps — lifecycle, destination resolver, outbox, translator, finalizer, verifier.
// Azione #9 (July 2026): DriveUploader removed from VoiceoverIntegrationDeps
// (replaced by Publisher in ProcessSegmentUseCase; post-commit cleanup now
// flows through the outbox handler via jobsoutbox.VoiceoverCleanupDriver).
type VoiceoverIntegrationDeps struct {
	LifecycleService  *lifecycle.Service
	AssetDestResolver asset.Resolver
	OutboxEnqueuer    TxOutboxEnqueuer
	// Translator is the per-promo-go translation callback; the
	// composition root wires it from scriptGen.TranslateText
	// (see build_bundles_voiceover.go). Fase 9 step 3 (July 2026)
	// declared the canonical type as `translation.TranslatorFunc`
	// directly (the prior voiceover-local typewriter alias
	// `type TranslatorFunc = translation.TranslatorFunc` has been
	// removed in step 3; tracking entry
	// architecture/deprecations.yaml#TRANSLATION-UNIFY).
	Translator translation.TranslatorFunc
	// Finalizer is the unified finalization port (P0.4 Fase 3a, July 2026).
	// Nil-safe: finalizeStage guards nil and surfaces a typed failure so
	// a partially-wired composition root fails at the per-language boundary
	// rather than mid-tx. Production wiring in build_bundles_voiceover.go
	// always supplies a non-nil finalizer.
	// PostCommitVerifier is the optional post-commit SQL verification
	// port (P0.4 Fase 4a, July 2026). After tx commit, finalizeStage
	// calls Verify to confirm durability. Nil-safe: skip verification.
	// ProcessSegment is the shared per-item pipeline runner (Azione #1,
	// July 2026). Wired in build_bundles_voiceover.go from the same
	// deps used by the per-item use case path. MANDATORY — the legacy
	// batch path delegates to ProcessSegmentUseCase.Execute instead of
	// calling synthesizeStage/destinationStage/finalizeStage inline.
	// ProcessItem is the canonical per-item voiceover use case that
	// the promo workflow routes through (P0-#3, July 2026). Wired in
	// build_bundles_voiceover.go from the same adapter surface the
	// legacy Service consumes. MANDATORY — a nil processItem at
	// construction time would silently disable the promo path;
	// GeneratePromo surfaces a typed error so the missing wire-up is
	// visible at the first promo call rather than as a hidden soft
	// no-op.
	// LanguageRegistry is the mandatory per-language capability surface
	// used to resolve EdgeTTSVoice. Missing or incomplete entries fail closed.
	LanguageRegistry asset.LanguageRegistry
}

// VoiceoverExecutionDeps groups the shared execution and durable-finalization
// ports used by both batch and promo voiceover flows.
type VoiceoverExecutionDeps struct {
	Finalizer          VoiceoverFinalizer
	PostCommitVerifier VoiceoverPostCommitVerifier
	ProcessSegment     *ProcessSegmentUseCase
	ProcessItem        VoiceoverItemExecutor
}

// NewService constructs a voiceover.Service from grouped dependency bundles.
//
// P0-#3 (July 2026): the canonical per-item use case (ProcessItem)
// is mandatory at composition time. A nil ProcessItem would silently
// disable the promo path at runtime (GeneratePromo's runtime check
// would surface a typed error on first call, but only at first call).
// P0-#3 promotes this to a fail-closed composition-time requirement
// (godlike/07 NO-FAKE-AVAILABILITY) so the missing wire-up is fixed
// at boot rather than at first promo call.
func NewService(deps VoiceoverDeps) *Service {
	if deps.Execution.ProcessItem == nil {
		panic("voiceover.NewService: ProcessItem is required (P0-#3 — the canonical per-item use case ProcessVoiceoverItemUseCase must be wired via build_bundles_voiceover.go)")
	}
	if deps.Integration.LanguageRegistry == nil {
		panic("voiceover.NewService: LanguageRegistry is required (the canonical voice registry must be wired at composition time)")
	}
	return &Service{
		cfg:                deps.Core.Cfg,
		voiceoverRepo:      deps.Persistence.Repo,
		ttsProvider:        deps.Generation.TTSProvider,
		outputDir:          deps.Core.OutputDir,
		log:                deps.Core.Log,
		assetDestResolver:  deps.Integration.AssetDestResolver,
		semanticTagger:     deps.Generation.SemanticTagger,
		outboxEnqueuer:     deps.Integration.OutboxEnqueuer,
		translator:         deps.Integration.Translator,
		finalizer:          deps.Execution.Finalizer,
		postCommitVerifier: deps.Execution.PostCommitVerifier,
		languageRegistry:   deps.Integration.LanguageRegistry,
		processSeg:         deps.Execution.ProcessSegment,
		processItem:        deps.Execution.ProcessItem,
	}
}

// RegisterHandler registers this service as a handler for voiceover jobs
// (both batch and promo).
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(appjobs.TypeVoiceoverBatch, appjobs.HandlerFunc(s.HandleJob))
		jobsSvc.RegisterHandler(appjobs.TypeVoiceoverPromo, appjobs.HandlerFunc(s.HandleJob))
		s.log.Info("registered voiceover job handlers (batch + promo)")
	}
}
