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
}

// TranslatorFunc translates text to a target language. Used by GeneratePromo.
// Deprecated: use translation.TranslatorFunc directly.
type TranslatorFunc = translation.TranslatorFunc

// NewService constructs a voiceover.Service. The optional dependencies
// (semanticTagger, translator, outboxEnqueuer) are wired at construction
// time. Pass nil for any dependency that the caller does not need — the
// service guards nil at every call site so the optional behaviour degrades
// gracefully (the IGnPath test path explicitly covers the no-outbox
// case in service_test.go).
//
// PR-VO-A3 (Outbox-based Qdrant indexing, June 2026): the
// clipIndexer ClipIndexFunc parameter is gone. The indexing handoff
// now flows through outboxEnqueuer TxOutboxEnqueuer, which writes
// inside the same SQLite transaction as swapVoiceoverRow.
//
// P1-2 boundary split (June 2026): the previous `*sql.DB` +
// `pythonScriptsDir` ctor args are gone — the *sql.DB handle is
// replaced by the canonical voiceoverRepo persistence port
// (voicedown of the *sql.DB raw usage in stages.go), and the
// TTS audio processor is constructed at the composition root
// (`internal/app/build_bundles_voiceover.go`) + injected via
// the TTSProvider port. `NewService` no longer imports
// `internal/infrastructure/audio` directly.
func NewService(
	cfg *config.Config,
	voiceoverRepo persistence.Repository,
	ttsProvider TTSProvider,
	outputDir string,
	log *zap.Logger,
	driveUploader DriveUploaderPort,
	lifecycleService *lifecycle.Service,
	assetDestResolver asset.Resolver,
	semanticTagger SemanticTaggerFunc,
	translator TranslatorFunc,
	outboxEnqueuer TxOutboxEnqueuer,
) *Service {
	return &Service{
		cfg:               cfg,
		voiceoverRepo:     voiceoverRepo,
		ttsProvider:       ttsProvider,
		outputDir:         outputDir,
		log:               log,
		driveUploader:     driveUploader,
		assetDestResolver: assetDestResolver,
		lifecycleService:  lifecycleService,
		semanticTagger:    semanticTagger,
		outboxEnqueuer:    outboxEnqueuer,
		translator:        translator,
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

func (s *Service) Cfg() *config.Config {
	return s.cfg
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
	if item.Error != "" {
		return nil, fmt.Errorf("%s (status: %s)", item.Error, item.Status)
	}

	return &VoiceoverResult{
		OK:          true,
		Voice:       item.Voice,
		Path:        item.LocalPath,
		DriveLink:   item.DriveLink,
		DriveFileID: item.DriveFileID,
	}, nil
}
