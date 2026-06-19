package generate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/contracts/scriptjobs"
	"github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
)

// GenerationService orchestrates script generation (use case layer).
// It owns validation, defaults, payload construction, and job enqueue
// for GenerateFromClips, GenerateWithImages, and GenerateBatch (async path).
type GenerationService struct {
	jobsSvc *jobs.Service
	cfg     *config.Config
	log     *zap.Logger
}

// NewGenerationService creates a new GenerationService.
func NewGenerationService(jobsSvc *jobs.Service, cfg *config.Config, log *zap.Logger) *GenerationService {
	return &GenerationService{
		jobsSvc: jobsSvc,
		cfg:     cfg,
		log:     log,
	}
}

// FromClipsResult is the application-layer result for clip-source generation.
type FromClipsResult struct {
	OK        bool
	JobID     string
	Status    string
	ClipCount int
}

// ── Public API ──────────────────────────────────────────────────────────────

// EnqueueFromClips validates, applies defaults, builds the payload, and
// enqueues a clip-source script generation job.
func (s *GenerationService) EnqueueFromClips(ctx context.Context, spec scriptjobs.GenerationSpec) (*FromClipsResult, error) {
	scriptsCfg := s.getScriptsConfig()

	if err := validateGeneration(&spec); err != nil {
		return nil, err
	}

	applyPreset(&spec, scriptsCfg, scriptjobs.PresetCustom)

	if spec.GenerateMetadata {
		spec.ExtractEntities = true
	}

	return s.enqueue(ctx, spec, scriptjobs.PresetCustom, "unified")
}

// EnqueueWithImages validates, applies defaults, builds the payload, and
// enqueues a generate-with-images job.
func (s *GenerationService) EnqueueWithImages(ctx context.Context, spec scriptjobs.GenerationSpec) (*FromClipsResult, error) {
	scriptsCfg := s.getScriptsConfig()

	if err := validateGeneration(&spec); err != nil {
		return nil, err
	}

	applyPreset(&spec, scriptsCfg, scriptjobs.PresetWithImages)

	return s.enqueue(ctx, spec, scriptjobs.PresetWithImages, "generate-with-images")
}

// ── Config & defaults ──────────────────────────────────────────────────────

func (s *GenerationService) getScriptsConfig() config.ScriptsConfig {
	if s.cfg != nil {
		return s.cfg.Scripts.WithDefaults()
	}
	return config.ScriptsConfig{}
}

// applyPreset applies the preset-specific defaults and feature flags.
//
// Common defaults (applied regardless of preset):
//   - language, tone, transcript_policy, ordering_strategy
//   - sentences_per_image, images_per_scene
//   - title/output_name, target_words
//
// PresetCustom: no feature flags are forced — the caller's values are
// respected (the HTTP layer maps every field from the request body).
//
// PresetWithImages: forces generate_scene_images=true, generate_voiceover=true,
// extract_entities=false, generate_metadata=false per the
// /generate-with-images endpoint contract.
func applyPreset(spec *scriptjobs.GenerationSpec, scriptsCfg config.ScriptsConfig, preset scriptjobs.Preset) {
	// Common defaults.
	if spec.Language == "" {
		spec.Language = scriptsCfg.DefaultLanguage
	}
	if spec.Tone == "" {
		spec.Tone = scriptsCfg.DefaultTone
	}
	if spec.TranscriptPolicy == "" {
		spec.TranscriptPolicy = "auto"
	}
	if spec.OrderingStrategy == "" {
		spec.OrderingStrategy = "auto"
	}
	if spec.SentencesPerImage <= 0 {
		spec.SentencesPerImage = 8
	}
	if spec.ImagesPerScene <= 0 {
		spec.ImagesPerScene = 1
	}

	title, outputName := resolveTitleAndOutputName(spec.Title, spec.Topic)
	spec.Title = title
	spec.OutputName = outputName

	spec.TargetWords = resolveTargetWords(spec.TargetWords, spec.MinWords, spec.Duration, scriptsCfg.MinWordFloor)

	// Preset-specific feature flags.
	switch preset {
	case scriptjobs.PresetWithImages:
		spec.GenerateSceneImages = true
		spec.GenerateVoiceover = true
		spec.ExtractEntities = false
		spec.GenerateMetadata = false
	case scriptjobs.PresetCustom:
		// No forced feature flags — the caller controls every flag.
	}
}

// ── Shared validation & enqueue ────────────────────────────────────────────

func validateGeneration(spec *scriptjobs.GenerationSpec) error {
	hasClips := spec.HasClips()
	hasText := spec.HasText()
	if !hasClips && !hasText {
		return fmt.Errorf("%w: provide clip_ids/num_clips for clip-aware mode, or topic/source_text for text-only mode", scriptjobs.ErrValidation)
	}
	if len(spec.ClipIDs) > 50 {
		return fmt.Errorf("%w: clip_ids cannot exceed 50 clips", scriptjobs.ErrValidation)
	}
	return nil
}

// enqueue builds the GeneratePayload, converts it to the job system's
// map[string]any format, and enqueues the script.generate_from_clips job.
func (s *GenerationService) enqueue(
	ctx context.Context,
	spec scriptjobs.GenerationSpec,
	preset scriptjobs.Preset,
	logLabel string,
) (*FromClipsResult, error) {
	if s.jobsSvc == nil {
		return nil, fmt.Errorf("jobs service not initialized")
	}

	payload := scriptjobs.NewGeneratePayload(preset, spec)
	payloadMap, err := toMap(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize payload: %w", err)
	}

	mode := "text-only"
	if spec.HasClips() {
		mode = "clip-aware"
	}
	s.log.Info("enqueuing "+logLabel+" job",
		zap.String("mode", mode),
		zap.Int("clip_count", len(spec.ClipIDs)),
		zap.String("title", spec.Title),
		zap.Bool("extract_entities", spec.ExtractEntities),
		zap.Bool("artlist_search", spec.ArtlistSearch),
		zap.Bool("stock_search", spec.StockSearch),
		zap.Bool("generate_metadata", spec.GenerateMetadata),
		zap.Int("images_per_scene", spec.ImagesPerScene),
		zap.Int("sentences_per_image", spec.SentencesPerImage),
	)

	job, err := s.jobsSvc.Enqueue(ctx, &jobs.EnqueueRequest{
		Type:       scriptjobs.JobTypeGenerateFromClips,
		Payload:    payloadMap,
		MaxRetries: 2,
	})
	if err != nil {
		s.log.Error("failed to enqueue "+logLabel+" job", zap.Error(err))
		return nil, err
	}

	clipCount := len(spec.ClipIDs)
	if clipCount == 0 && spec.NumClips > 0 {
		clipCount = spec.NumClips
	}

	return &FromClipsResult{
		OK:        true,
		JobID:     job.ID,
		Status:    string(job.Status),
		ClipCount: clipCount,
	}, nil
}

// toMap converts a typed value to map[string]any via JSON round-trip.
// Used to bridge the typed GenerationSpec contract with the job system's
// map[string]any Payload field.
func toMap(v interface{}) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// ── Shared helpers ─────────────────────────────────────────────────────────

func resolveTitleAndOutputName(topicTitle, topic string) (resolvedTitle, outputName string) {
	resolvedTitle = strings.TrimSpace(topicTitle)
	if resolvedTitle == "" {
		resolvedTitle = strings.TrimSpace(topic)
	}
	if resolvedTitle == "" {
		resolvedTitle = "Generated Script"
	}
	outputName = textutil.Slugify(resolvedTitle)
	if outputName == "" {
		outputName = "generated-script"
	}
	return resolvedTitle, outputName
}

func resolveTargetWords(targetWords, minWords, duration, minWordFloor int) int {
	tw := targetWords
	if tw <= 0 {
		tw = minWords
	}
	if tw <= 0 && duration > 0 {
		tw = (duration * 150) / 60
	}
	if tw <= 0 {
		tw = minWordFloor
	}
	if tw <= 0 {
		tw = 1200
	}
	return tw
}
