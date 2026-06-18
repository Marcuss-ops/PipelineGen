package voiceover

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/core/destination"
	"github.com/Marcuss-ops/PipelineGen/internal/core/lifecycle"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/audioasset"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"

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
	assetDestResolver destination.Resolver
	audioProcessor    *audioasset.Processor
	lifecycleService  *lifecycle.Service
	semanticTagger    SemanticTaggerFunc
	clipIndexer       ClipIndexFunc  // optional: triggers embedding + Qdrant upsert
	translator        TranslatorFunc // optional: translates text to target language
}

// TranslatorFunc translates text to a target language. Used by GeneratePromo.
type TranslatorFunc func(ctx context.Context, text, targetLanguage string) (string, error)

func NewService(
	cfg *config.Config,
	db *sql.DB,
	pythonScriptsDir string,
	outputDir string,
	log *zap.Logger,
	driveUploader *drive.Uploader,
	lifecycleService *lifecycle.Service,
	assetDestResolver destination.Resolver,
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
	}
}

// RegisterHandler registers this service as a handler for voiceover jobs
func (s *Service) RegisterHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(jobservice.JobTypeVoiceoverBatch, s.HandleJob)
		s.log.Info("registered voiceover job handler")
	}
}

// SetSemanticTagger sets the callback function for semantic metadata enrichment.
// Must be called after construction to enable search_text/tags on voiceovers.
func (s *Service) SetSemanticTagger(fn SemanticTaggerFunc) {
	s.semanticTagger = fn
}

// SetClipIndexer sets the callback for triggering embedding generation + Qdrant upsert.
// Called after semantic enrichment to make voiceovers searchable via semantic search.
func (s *Service) SetClipIndexer(fn ClipIndexFunc) {
	s.clipIndexer = fn
}

// SetTranslator sets the translation callback for promo voiceover generation.
func (s *Service) SetTranslator(fn TranslatorFunc) {
	s.translator = fn
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
