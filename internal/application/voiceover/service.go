package voiceover

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	ptrutil "github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"

	"go.uber.org/zap"
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
	ttsProvider       TTSProvider
	outputDir         string
	log               *zap.Logger
	// (BLOC5.3 processItemUseCase field REMOVED June 2026 cutover:
	// the canonical per-item voiceover pipeline was never committed
	// in this branch — voiceover/promo.go now routes via legacy
	// Service.GenerateWithDestination. The VoiceoverItemExecutor
	// interface in ports.go is retained for the BLOC5.4 follow-up
	// that will land the concrete pipeline.)
	// driveUploader is a narrow structural port (PR-VO-B1, June 2026):
	// voiceover no longer imports infrastructure/drive. DeleteFile
	// is the only method the service uses today (post-commit cleanup
	// of OLD voiceover Drive files in replace-mode). The production
	// concrete *drive.Uploader is wrapped by app/voiceoverDriveAdapter.
	driveUploader     DriveUploaderPort
	assetDestResolver asset.Resolver
	lifecycleService  *lifecycle.Service
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
}

// TranslatorFunc translates text to a target language. Used by GeneratePromo.
// Deprecated: use translation.TranslatorFunc directly.
// Removal plan: 15 consumer sites verified (2026-06-30). Migrate callers
// in Wave 23, then remove the alias. Tracked in architecture/deprecations.yaml.
type TranslatorFunc = translation.TranslatorFunc

// VoiceoverDeps groups constructor dependencies by real capability.
// Replaces the 11-param flat constructor with 4 grouped bundles.
type VoiceoverDeps struct {
	Core        VoiceoverCoreDeps
	Persistence VoiceoverPersistenceDeps
	Generation  VoiceoverGenerationDeps
	Integration VoiceoverIntegrationDeps
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
// (June 2026 cutover): the BLOC5.3 ProcessItemUseCase field was
// removed because the canonical per-item pipeline was never landed
// in this branch. The VoiceoverItemExecutor interface in ports.go is
// retained for the BLOC5.4 follow-up. The promo workflow
// (voiceover/promo.go) routes through legacy Service.GenerateWithDestination.
type VoiceoverGenerationDeps struct {
	TTSProvider    TTSProvider
	SemanticTagger SemanticTaggerFunc
}

// VoiceoverIntegrationDeps — Drive, lifecycle, destination resolver, outbox, translator, finalizer.
type VoiceoverIntegrationDeps struct {
	DriveUploader     DriveUploaderPort
	LifecycleService  *lifecycle.Service
	AssetDestResolver asset.Resolver
	OutboxEnqueuer    TxOutboxEnqueuer
	Translator        TranslatorFunc
	// Finalizer is the unified finalization port (P0.4 Fase 3a, July 2026).
	// Nil-safe: finalizeStage guards nil and surfaces a typed failure so
	// a partially-wired composition root fails at the per-language boundary
	// rather than mid-tx. Production wiring in build_bundles_voiceover.go
	// always supplies a non-nil finalizer.
	Finalizer VoiceoverFinalizer
}

// NewService constructs a voiceover.Service from grouped dependency bundles.
func NewService(deps VoiceoverDeps) *Service {
	return &Service{
		cfg:               deps.Core.Cfg,
		voiceoverRepo:     deps.Persistence.Repo,
		ttsProvider:       deps.Generation.TTSProvider,
		outputDir:         deps.Core.OutputDir,
		log:               deps.Core.Log,
		driveUploader:     deps.Integration.DriveUploader,
		assetDestResolver: deps.Integration.AssetDestResolver,
		lifecycleService:  deps.Integration.LifecycleService,
		semanticTagger:    deps.Generation.SemanticTagger,
		outboxEnqueuer: deps.Integration.OutboxEnqueuer,
		translator:     deps.Integration.Translator,
		finalizer:      deps.Integration.Finalizer,
	}
}

// RegisterHandler registers this service as a handler for voiceover jobs
// (both batch and promo).
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(appjobs.TypeVoiceoverBatch, s.HandleJob)
		jobsSvc.RegisterHandler(appjobs.TypeVoiceoverPromo, s.HandleJob)
		s.log.Info("registered voiceover job handlers (batch + promo)")
	}
}

func (s *Service) GenerateWithDestination(ctx context.Context, text, language, filename string, dest *DestinationRequest) (*VoiceoverResult, error) {
	req := &BatchRequest{
		Text:             text,
		Languages:        []string{language},
		FilenameTemplate: filename,
		RemoveSilence:    ptrutil.Bool(false),
		Strategy:         "replace",
		Destination:      dest,
	}
	resp, err := s.GenerateBatch(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(resp.Items) == 0 {
		return nil, fmt.Errorf("no voiceover generated")
	}

	item := resp.Items[0]
	if !item.isSuccessful() {
		msg := item.Error
		if msg == "" {
			msg = "voiceover generation did not complete"
		}
		return nil, fmt.Errorf("%s (status: %s)", msg, item.Status)
	}

	return &VoiceoverResult{
		OK:          true,
		Voice:       item.Voice,
		Path:        item.LocalPath,
		DriveLink:   item.DriveLink,
		DriveFileID: item.DriveFileID,
	}, nil
}
