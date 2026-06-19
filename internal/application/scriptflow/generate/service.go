package generate

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

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

// ── Public API ──────────────────────────────────────────────────────────────

// clipScriptDefaults holds the shared request defaults for clip/script generation.
type clipScriptDefaults struct {
	hasClips            bool
	extractEntities     bool
	generateSceneImages bool
	generateMetadata    bool
	generateVoiceover   bool
}

// EnqueueFromClips validates, applies defaults, builds the payload, and
// enqueues a clip-source script generation job.
func (s *GenerationService) EnqueueFromClips(ctx context.Context, cmd *FromClipsCommand) (*FromClipsResult, error) {
	scriptsCfg := s.getScriptsConfig()
	hasClips := len(cmd.ClipIDs) > 0 || cmd.NumClips > 0

	if err := validateClipScript(cmd.Topic, cmd.SourceText, cmd.ClipIDs, hasClips); err != nil {
		return nil, err
	}

	applyDefaultsFromClips(cmd, scriptsCfg)

	if cmd.GenerateMetadata {
		cmd.ExtractEntities = true
	}

	return s.enqueueClipScript(ctx, cmd.Topic, cmd.SourceText, cmd.Guidelines,
		cmd.ClipIDs, cmd.NumClips, cmd.Title, cmd.OutputName,
		cmd.Language, cmd.Tone, cmd.Model, cmd.DriveFolderID,
		cmd.TargetWords, cmd.Duration, cmd.MinWords,
		cmd.SentencesPerImage, cmd.ImagesPerScene,
		cmd.Style, cmd.ArtlistSearch, cmd.StockSearch,
		cmd.Languages, cmd.TranscriptPolicy, cmd.OrderingStrategy,
		cmd.SaveToDB, cmd.GenerateTimeline, cmd.ForceRefresh,
		cmd.PromptVersion, cmd.EditorPromptVersion, cmd.QAPromptVersion,
		cmd.VoiceoverGroup, cmd.VoiceoverFolderID,
		cmd.MinQualityScore, cmd.MinTranscriptWords,
		clipScriptDefaults{
			hasClips:            hasClips,
			extractEntities:     cmd.ExtractEntities,
			generateSceneImages: cmd.GenerateSceneImages,
			generateMetadata:    cmd.GenerateMetadata,
			generateVoiceover:   cmd.GenerateVoiceover,
		},
		"unified",
	)
}

// EnqueueWithImages validates, applies defaults, builds the payload, and
// enqueues a generate-with-images job.
func (s *GenerationService) EnqueueWithImages(ctx context.Context, cmd *WithImagesCommand) (*FromClipsResult, error) {
	scriptsCfg := s.getScriptsConfig()
	hasClips := len(cmd.ClipIDs) > 0 || cmd.NumClips > 0

	if err := validateClipScript(cmd.Topic, cmd.SourceText, cmd.ClipIDs, hasClips); err != nil {
		return nil, err
	}

	applyDefaultsWithImages(cmd, scriptsCfg)

	return s.enqueueClipScript(ctx, cmd.Topic, cmd.SourceText, cmd.Guidelines,
		cmd.ClipIDs, cmd.NumClips, cmd.Title, cmd.OutputName,
		cmd.Language, cmd.Tone, cmd.Model, cmd.DriveFolderID,
		cmd.TargetWords, cmd.Duration, cmd.MinWords,
		cmd.SentencesPerImage, cmd.ImagesPerScene,
		cmd.Style, cmd.ArtlistSearch, cmd.StockSearch,
		cmd.Languages, cmd.TranscriptPolicy, cmd.OrderingStrategy,
		cmd.SaveToDB, cmd.GenerateTimeline, cmd.ForceRefresh,
		cmd.PromptVersion, cmd.EditorPromptVersion, cmd.QAPromptVersion,
		cmd.VoiceoverGroup, cmd.VoiceoverFolderID,
		cmd.MinQualityScore, cmd.MinTranscriptWords,
		clipScriptDefaults{
			hasClips:            hasClips,
			extractEntities:     false, // forced for this endpoint
			generateSceneImages: true,  // forced for this endpoint
			generateMetadata:    false, // forced for this endpoint
			generateVoiceover:   cmd.GenerateVoiceover,
		},
		"generate-with-images",
	)
}

// ── Batch ───────────────────────────────────────────────────────────────────

// EnqueueBatch validates, applies defaults, builds the payload, and enqueues
// an async batch generation job. For sync batch execution, the caller should
// use the handler's ExecuteBatchGeneration directly (PR 4 will extract this).
//
// TODO(PR-4): implement when batch async path is extracted from ScriptFlowHandler.
func (s *GenerationService) EnqueueBatch(ctx context.Context, cmd *FromClipsCommand) (*BatchResult, error) {
	return nil, fmt.Errorf("not implemented — PR 4 will extract batch async path from ScriptFlowHandler")
}

// ── Config & defaults ──────────────────────────────────────────────────────

func (s *GenerationService) getScriptsConfig() config.ScriptsConfig {
	if s.cfg != nil {
		return s.cfg.Scripts.WithDefaults()
	}
	return config.ScriptsConfig{}
}

func applyDefaultsFromClips(cmd *FromClipsCommand, scriptsCfg config.ScriptsConfig) {
	if cmd.Language == "" {
		cmd.Language = scriptsCfg.DefaultLanguage
	}
	if cmd.Tone == "" {
		cmd.Tone = scriptsCfg.DefaultTone
	}
	if cmd.TranscriptPolicy == "" {
		cmd.TranscriptPolicy = "auto"
	}
	if cmd.OrderingStrategy == "" {
		cmd.OrderingStrategy = "auto"
	}
	if cmd.SentencesPerImage <= 0 {
		cmd.SentencesPerImage = 8
	}
	if cmd.ImagesPerScene <= 0 {
		cmd.ImagesPerScene = 1
	}
	cmd.GenerateSceneImages = true

	title, outputName := resolveTitleAndOutputName(cmd.Title, cmd.Topic)
	cmd.Title = title
	cmd.OutputName = outputName

	cmd.TargetWords = resolveTargetWords(cmd.TargetWords, cmd.MinWords, cmd.Duration, scriptsCfg.MinWordFloor)
}

func applyDefaultsWithImages(cmd *WithImagesCommand, scriptsCfg config.ScriptsConfig) {
	if cmd.Language == "" {
		cmd.Language = scriptsCfg.DefaultLanguage
	}
	if cmd.Tone == "" {
		cmd.Tone = scriptsCfg.DefaultTone
	}
	if cmd.TranscriptPolicy == "" {
		cmd.TranscriptPolicy = "auto"
	}
	if cmd.OrderingStrategy == "" {
		cmd.OrderingStrategy = "auto"
	}
	if cmd.SentencesPerImage <= 0 {
		cmd.SentencesPerImage = 8
	}
	if cmd.ImagesPerScene <= 0 {
		cmd.ImagesPerScene = 1
	}
	cmd.GenerateVoiceover = true

	title, outputName := resolveTitleAndOutputName(cmd.Title, cmd.Topic)
	cmd.Title = title
	cmd.OutputName = outputName

	cmd.TargetWords = resolveTargetWords(cmd.TargetWords, cmd.MinWords, cmd.Duration, scriptsCfg.MinWordFloor)
}

// ── Shared validation & enqueue ────────────────────────────────────────────

func validateClipScript(topic, sourceText string, clipIDs []string, hasClips bool) error {
	hasTopic := strings.TrimSpace(topic) != "" || strings.TrimSpace(sourceText) != ""
	if !hasClips && !hasTopic {
		return fmt.Errorf("provide clip_ids/num_clips for clip-aware mode, or topic/source_text for text-only mode")
	}
	if len(clipIDs) > 50 {
		return fmt.Errorf("clip_ids cannot exceed 50 clips")
	}
	return nil
}

// enqueueClipScript is the shared job enqueue path for FromClips and WithImages.
// Both endpoints produce a script.generate_from_clips job with the same shape.
func (s *GenerationService) enqueueClipScript(
	ctx context.Context,
	topic, sourceText, guidelines string,
	clipIDs []string, numClips int,
	title, outputName, language, tone, model, driveFolderID string,
	targetWords, duration, minWords, sentencesPerImage, imagesPerScene int,
	style string, artlistSearch, stockSearch bool,
	languages []string, transcriptPolicy, orderingStrategy string,
	saveToDB, generateTimeline, forceRefresh bool,
	promptVersion, editorPromptVersion, qaPromptVersion string,
	voiceoverGroup, voiceoverFolderID string,
	minQualityScore *float64, minTranscriptWords *int,
	flags clipScriptDefaults,
	logLabel string,
) (*FromClipsResult, error) {
	if s.jobsSvc == nil {
		return nil, fmt.Errorf("jobs service not initialized")
	}

	payload := map[string]any{
		"topic":                 topic,
		"source_text":           sourceText,
		"guidelines":            guidelines,
		"clip_ids":              clipIDs,
		"num_clips":             numClips,
		"title":                 title,
		"output_name":           outputName,
		"language":              language,
		"tone":                  tone,
		"model":                 model,
		"target_words":          targetWords,
		"duration":              duration,
		"min_words":             minWords,
		"extract_entities":      flags.extractEntities,
		"generate_scene_images": flags.generateSceneImages,
		"generate_metadata":     flags.generateMetadata,
		"style":                 style,
		"artlist_search":        artlistSearch,
		"stock_search":          stockSearch,
		"languages":             languages,
		"transcript_policy":     transcriptPolicy,
		"ordering_strategy":     orderingStrategy,
		"save_to_db":            saveToDB,
		"generate_timeline":     generateTimeline,
		"force_refresh":         forceRefresh,
		"prompt_version":        promptVersion,
		"editor_prompt_version": editorPromptVersion,
		"qa_prompt_version":     qaPromptVersion,
		"drive_folder_id":       driveFolderID,
		"sentences_per_image":   sentencesPerImage,
		"images_per_scene":      imagesPerScene,
		"generate_voiceover":    flags.generateVoiceover,
		"voiceover_group":       voiceoverGroup,
		"voiceover_folder_id":   voiceoverFolderID,
	}
	if minQualityScore != nil {
		payload["min_quality_score"] = *minQualityScore
	}
	if minTranscriptWords != nil {
		payload["min_transcript_words"] = *minTranscriptWords
	}

	mode := "text-only"
	if flags.hasClips {
		mode = "clip-aware"
	}
	s.log.Info("enqueuing "+logLabel+" job",
		zap.String("mode", mode),
		zap.Int("clip_count", len(clipIDs)),
		zap.String("title", title),
		zap.Bool("extract_entities", flags.extractEntities),
		zap.Bool("artlist_search", artlistSearch),
		zap.Bool("stock_search", stockSearch),
		zap.Bool("generate_metadata", flags.generateMetadata),
		zap.Int("images_per_scene", imagesPerScene),
		zap.Int("sentences_per_image", sentencesPerImage),
	)

	job, err := s.jobsSvc.Enqueue(ctx, &jobs.EnqueueRequest{
		Type:       "script.generate_from_clips",
		Payload:    payload,
		MaxRetries: 2,
	})
	if err != nil {
		s.log.Error("failed to enqueue "+logLabel+" job", zap.Error(err))
		return nil, err
	}

	clipCount := len(clipIDs)
	if clipCount == 0 && numClips > 0 {
		clipCount = numClips
	}

	return &FromClipsResult{
		OK:        true,
		JobID:     job.ID,
		Status:    string(job.Status),
		ClipCount: clipCount,
	}, nil
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
