package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/core/jobs"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// GenerateFromClips is the unified async endpoint for ALL script generation.
// POST /api/script/generate-from-clips
//
// Supports both:
//   - Text-only mode: provide topic/source_text (num_clips=0, no clip_ids)
//   - Clip-aware mode: provide clip_ids or num_clips > 0
//
// Always async — returns job_id immediately. Google Doc sempre creato.
func (h *ScriptFlowHandler) GenerateFromClips(c *gin.Context) {
	if h.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}

	scriptsCfg := config.ScriptsConfig{}
	if h.cfg != nil {
		scriptsCfg = h.cfg.Scripts.WithDefaults()
	}

	req, ok := apiutil.BindJSON[GenerateFromClipsRequest](c)
	if !ok {
		return
	}

	// ── Validation: require either clip_ids/num_clips OR topic ─────────────
	hasClips := len(req.ClipIDs) > 0 || req.NumClips > 0
	hasTopic := strings.TrimSpace(req.Topic) != "" || strings.TrimSpace(req.SourceText) != ""

	if !hasClips && !hasTopic {
		apiutil.BadRequest(c, "provide clip_ids/num_clips for clip-aware mode, or topic/source_text for text-only mode")
		return
	}

	if len(req.ClipIDs) > 50 {
		apiutil.BadRequest(c, "clip_ids cannot exceed 50 clips")
		return
	}

	// ── Apply defaults ─────────────────────────────────────────────────────
	if req.Language == "" {
		req.Language = scriptsCfg.DefaultLanguage
	}
	if req.Tone == "" {
		req.Tone = scriptsCfg.DefaultTone
	}
	if req.TranscriptPolicy == "" {
		req.TranscriptPolicy = "auto"
	}
	if req.OrderingStrategy == "" {
		req.OrderingStrategy = "auto"
	}
	if req.SentencesPerImage <= 0 {
		req.SentencesPerImage = 8
	}
	if req.ImagesPerScene <= 0 {
		req.ImagesPerScene = 1
	}
	// Behaviour alignment with /api/script/generate-with-images (June 2026
	// endpoint-compat request): /generate-from-clips now matches /generate-with
	// -images in forcing scene-image generation on by default so the `scenes[]`
	// array is always populated with AI-generated scenes + intro/outro.
	// Clients can still pass `false` explicitly — but they typically want the
	// same output shape as `generate-with-images` from this endpoint.
	req.GenerateSceneImages = true

	title, outputName := resolveTitleAndOutputName(req.Title, req.Topic)
	req.Title = title
	req.OutputName = outputName

	req.TargetWords = resolveTargetWords(req.TargetWords, req.MinWords, req.Duration, scriptsCfg.MinWordFloor)

	// generate_metadata implies extract_entities
	if req.GenerateMetadata {
		req.ExtractEntities = true
	}

	// ── Build payload for the job system ───────────────────────────────────
	payload := map[string]any{
		"topic":                 req.Topic,
		"source_text":           req.SourceText,
		"guidelines":            req.Guidelines,
		"clip_ids":              req.ClipIDs,
		"num_clips":             req.NumClips,
		"title":                 req.Title,
		"output_name":           req.OutputName,
		"language":              req.Language,
		"tone":                  req.Tone,
		"model":                 req.Model,
		"target_words":          req.TargetWords,
		"duration":              req.Duration,
		"min_words":             req.MinWords,
		"extract_entities":      req.ExtractEntities,
		"generate_scene_images": req.GenerateSceneImages,
		"style":                 req.Style,
		"artlist_search":        req.ArtlistSearch,
		"stock_search":          req.StockSearch,
		"generate_metadata":     req.GenerateMetadata,
		"languages":             req.Languages,
		"transcript_policy":     req.TranscriptPolicy,
		"ordering_strategy":     req.OrderingStrategy,
		"save_to_db":            req.SaveToDB,
		"generate_timeline":     req.GenerateTimeline,
		"force_refresh":         req.ForceRefresh,
		"prompt_version":        req.PromptVersion,
		"editor_prompt_version": req.EditorPromptVersion,
		"qa_prompt_version":     req.QAPromptVersion,
		"drive_folder_id":       req.DriveFolderID,
		"sentences_per_image":   req.SentencesPerImage,
		"images_per_scene":      req.ImagesPerScene,
		"generate_voiceover":    req.GenerateVoiceover,
		"voiceover_group":       req.VoiceoverGroup,
		"voiceover_folder_id":   req.VoiceoverFolderID,
	}
	if req.MinQualityScore != nil {
		payload["min_quality_score"] = *req.MinQualityScore
	}
	if req.MinTranscriptWords != nil {
		payload["min_transcript_words"] = *req.MinTranscriptWords
	}

	mode := "text-only"
	if hasClips {
		mode = "clip-aware"
	}
	h.log.Info("enqueuing unified script generation job",
		zap.String("mode", mode),
		zap.Int("clip_count", len(req.ClipIDs)),
		zap.String("title", req.Title),
		zap.Bool("extract_entities", req.ExtractEntities),
		zap.Bool("artlist_search", req.ArtlistSearch),
		zap.Bool("stock_search", req.StockSearch),
		zap.Bool("generate_metadata", req.GenerateMetadata),
	)

	job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:       models.JobType(jobs.JobTypeClipScriptGenerate),
		Payload:    payload,
		MaxRetries: 2,
	})
	if err != nil {
		h.log.Error("failed to enqueue script generation job", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	clipCount := len(req.ClipIDs)
	if clipCount == 0 && req.NumClips > 0 {
		clipCount = req.NumClips
	}

	apiutil.OK(c, GenerateFromClipsResponse{
		OK:        true,
		JobID:     job.ID,
		Status:    string(job.Status),
		ClipCount: clipCount,
	})
}
