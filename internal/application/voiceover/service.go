package voiceover

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
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

	// PR 2 (June 2026): the canonical use case for voiceover generation.
	// Old methods (Generate, GenerateBatch, GenerateWithDestination,
	// GeneratePromo) delegate to this use case. Built in NewService via
	// adapters that wrap the existing infrastructure.
	useCase *GenerateVoiceoverUseCase
}

// TranslatorFunc translates text to a target language. Used by GeneratePromo.
type TranslatorFunc func(ctx context.Context, text, targetLanguage string) (string, error)

// NewService constructs a voiceover.Service. The optional callbacks
// (semanticTagger, translator, clipIndexer) are wired at construction
// time. Pass nil for any callback that the caller does not need — the
// service guards nil at call sites so the optional behaviour degrades
// gracefully.
//
// PR 2 (June 2026): the service also builds and wires the canonical
// GenerateVoiceoverUseCase via adapters. Old methods delegate to it.
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

	svc := &Service{
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

	// PR 2: wire the canonical use case via adapters.
	ttsAdapter := newTTSProviderAdapter(audioProcessor, log)
	destAdapter := newDestinationResolverAdapter(assetDestResolver, log)
	voiceAdapter := newVoiceRegistryAdapter()

	svc.useCase = NewGenerateVoiceoverUseCase(GenerateVoiceoverDeps{
		VoiceRegistry:       voiceAdapter,
		DestinationResolver: destAdapter,
		TTSProvider:         ttsAdapter,
		LifecycleService:    lifecycleService,
		SemanticTagger:      semanticTagger,
		ClipIndexer:         clipIndexer,
		OutputDir:           outputDir,
		Log:                 log,
	})

	return svc
}

// RegisterHandler registers this service as the handler for
// voiceover.generate jobs (PR 3: voiceover.batch and voiceover.promo removed).
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(appjobs.TypeVoiceoverGenerate, s.HandleJob)
		s.log.Info("registered voiceover.generate job handler")
	}
}

func (s *Service) Cfg() *config.Config {
	return s.cfg
}

func (s *Service) DB() *sql.DB {
	return s.db
}

// UseCase returns the canonical GenerateVoiceoverUseCase. Returns nil if the
// service was constructed without adapters (legacy bootstrap path).
func (s *Service) UseCase() *GenerateVoiceoverUseCase { return s.useCase }

// ── PR 2 delegation helpers ────────────────────────────────────────────

// toCommandFromLegacy converts a legacy (text, language, dest) tuple into
// a domain.GenerateVoiceoverCommand for use case delegation.
//
// Bare language codes (e.g. "en", "it") are normalized to their regional
// equivalents (e.g. "en-US", "it-IT") so they pass the BCP-47 validation
// in the use case.
func (s *Service) toCommandFromLegacy(text, language string, dest *DestinationRequest, forceRegenerate bool) domain.GenerateVoiceoverCommand {
	locale := normalizeLegacyLocale(language)
	cmd := domain.GenerateVoiceoverCommand{
		Text:            text,
		Locale:          locale,
		ForceRegenerate: forceRegenerate,
	}
	if dest != nil && dest.FolderID != "" {
		cmd.Destination = domain.DestinationRef{FolderID: dest.FolderID}
	} else if s.cfg != nil && s.cfg.Drive.VoiceoverFolder() != "" {
		cmd.Destination = domain.DestinationRef{FolderID: s.cfg.Drive.VoiceoverFolder()}
	}
	return cmd
}

// normalizeLegacyLocale maps bare language codes (e.g. "en", "it") to
// their regional BCP-47 equivalents (e.g. "en-US", "it-IT") so they pass
// the use case's IsSupported check. Codes already in xx-XX form are
// returned unchanged.
func normalizeLegacyLocale(language string) domain.Locale {
	bareToRegional := map[string]string{
		"en": "en-US",
		"es": "es-ES",
		"fr": "fr-FR",
		"de": "de-DE",
		"it": "it-IT",
		"pt": "pt-BR",
		"pl": "pl-PL",
		"nl": "nl-NL",
		"ja": "ja-JP",
		"ko": "ko-KR",
		"ru": "ru-RU",
		"tr": "tr-TR",
		"id": "id-ID",
	}
	if regional, ok := bareToRegional[language]; ok {
		return domain.Locale(regional)
	}
	return domain.Locale(language)
}

// toLegacyResult converts a domain.VoiceoverResult into the legacy
// VoiceoverResult response type.
func (s *Service) toLegacyResult(result *domain.VoiceoverResult) *VoiceoverResult {
	if result == nil {
		return &VoiceoverResult{Error: "no result"}
	}
	return &VoiceoverResult{
		OK:          true,
		Voice:       result.Voice,
		Path:        result.LocalPath,
		DriveLink:   result.DriveLink,
		DriveFileID: result.DriveFileID,
	}
}

// toLegacyBatchItem converts a domain.VoiceoverResult into the legacy
// BatchItem type.
func (s *Service) toLegacyBatchItem(result *domain.VoiceoverResult, language string) BatchItem {
	item := BatchItem{
		ID:       result.ID,
		Language: language,
		Voice:    result.Voice,
		Filename: result.Filename,
		Status:   "processed",
	}
	if result.LocalPath != "" {
		item.LocalPath = result.LocalPath
	}
	if result.DriveLink != "" {
		item.DriveLink = result.DriveLink
	}
	if result.DriveFileID != "" {
		item.DriveFileID = result.DriveFileID
	}
	if result.FileHash != "" {
		item.FileHash = result.FileHash
	}
	return item
}

// ── Legacy methods (delegated to use case) ─────────────────────────────

func (s *Service) GenerateWithDestination(ctx context.Context, text, language, filename string, dest *DestinationRequest) (*VoiceoverResult, error) {
	// PR 2: delegate to the canonical use case when wired.
	if s.useCase != nil {
		cmd := s.toCommandFromLegacy(text, language, dest, true)
		result, err := s.useCase.Execute(ctx, cmd)
		if err != nil {
			return nil, err
		}
		return s.toLegacyResult(result), nil
	}

	// Legacy fallback: construct a BatchRequest and delegate to GenerateBatch.
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
