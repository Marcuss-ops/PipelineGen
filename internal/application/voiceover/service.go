package voiceover

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	ptrutil "github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"

	"go.uber.org/zap"
)

// SemanticTaggerFunc is a function that calls the Python semantic tagger.
// Defined as a callback to avoid circular imports with the images package.
type SemanticTaggerFunc func(ctx context.Context, prompt, style, mediaType, generator string) (*SemanticTaggerResult, error)

// ClipIndexFunc is a callback that triggers embedding generation + Qdrant upsert
// for a given asset. Defined as a callback to avoid circular imports with clipindexer.
// Implementations should call clipindexer.IndexClip() or equivalent.
type ClipIndexFunc func(ctx context.Context, assetID string) error

// SemanticTaggerResult mirrors the semantic tagger output for voiceover use.
type SemanticTaggerResult struct {
	SearchText string   `json:"search_text"`
	Tags       []string `json:"tags"`
	Subjects   []string `json:"subjects"`
	Mood       []string `json:"mood"`
}

// AudioProcessor is the port for TTS audio generation.
// TODO(PR6): move to internal/application/audio/postprocessor when PR 5 use case is restored.
// Currently bridges the concrete audioasset.Processor used by processLanguage (legacy path).
type AudioProcessor interface {
	Generate(ctx context.Context, input *audioasset.AudioInput) (*audioasset.AudioResult, error)
}

// Ensure concrete processor satisfies the interface.
var _ AudioProcessor = (*audioasset.Processor)(nil)

type Service struct {
	cfg               *config.Config
	db                *sql.DB
	pythonScriptsDir  string
	outputDir         string
	log               *zap.Logger
	driveUploader     *drive.Uploader
	assetDestResolver asset.Resolver
	// audioProcessor holds the TTS audio processor.
	// TODO(PR6): replace with TTSProvider port from use case when PR 5 is restored.
	audioProcessor   AudioProcessor
	lifecycleService *lifecycle.Service
	semanticTagger   SemanticTaggerFunc
	clipIndexer      ClipIndexFunc  // optional: triggers embedding + Qdrant upsert
	translator       translation.TranslatorFunc // Deprecated: moved to translation pkg, kept for GeneratePromo compat
}

// TranslatorFunc translates text to a target language. Used by GeneratePromo.
// Deprecated: use translation.TranslatorFunc directly.
type TranslatorFunc = translation.TranslatorFunc

// NewService constructs a voiceover.Service. The optional callbacks
// (semanticTagger, translator, clipIndexer) are wired at construction
// time. Pass nil for any callback that the caller does not need — the
// service guards nil at call sites so the optional behaviour degrades
// gracefully.
func NewService(
	cfg *config.Config,
	db *sql.DB,
	pythonScriptsDir string,
	outputDir string,
	log *zap.Logger,
	driveUploader *drive.Uploader,
	lifecycleService *lifecycle.Service,
	assetDestResolver asset.Resolver,
	semanticTagger SemanticTaggerFunc,
	translator TranslatorFunc,
	clipIndexer ClipIndexFunc,
) *Service {
	// Create audio asset processor
	audioProcessor := audioasset.NewProcessor(
		pythonScriptsDir,
		driveUploader,
		assetDestResolver,
		log,
	)

	return &Service{
		cfg:               cfg,
		db:                db,
		pythonScriptsDir:  pythonScriptsDir,
		outputDir:         outputDir,
		log:               log,
		driveUploader:     driveUploader,
		assetDestResolver: assetDestResolver,
		audioProcessor:    audioProcessor,
		lifecycleService:  lifecycleService,
		semanticTagger:    semanticTagger,
		translator:        translator,
		clipIndexer:       clipIndexer,
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

func (s *Service) DB() *sql.DB {
	return s.db
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
