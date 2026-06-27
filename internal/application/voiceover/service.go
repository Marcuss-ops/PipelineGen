package voiceover

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
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

type Service struct {
	cfg               *config.Config
	db                *sql.DB
	pythonScriptsDir  string
	outputDir         string
	log               *zap.Logger
	driveUploader     *drive.Uploader
	assetDestResolver asset.Resolver
	// audioProcessor holds the concrete TTS processor from
	// internal/infrastructure/audio/. A future hosted-TTS adapter can be
	// swapped in via the same constructor.
	audioProcessor   *audioasset.Processor
	lifecycleService *lifecycle.Service
	semanticTagger   SemanticTaggerFunc
	clipIndexer      ClipIndexFunc  // optional: triggers embedding + Qdrant upsert
	translator       TranslatorFunc // optional: translates text to target language
}

// TranslatorFunc translates text to a target language. Used by GeneratePromo.
type TranslatorFunc func(ctx context.Context, text, targetLanguage string) (string, error)

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
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(appjobs.TypeVoiceoverBatch, s.HandleJob)
		s.log.Info("registered voiceover job handler")
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
