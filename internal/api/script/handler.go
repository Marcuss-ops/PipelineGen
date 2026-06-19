// Package script provides the HTTP transport layer for script generation endpoints.
//
// This package is intentionally thin — it contains only the Handler struct,
// route registration, and request/response mappings. Business orchestration
// (generation, curation, scene planning, document building) lives in
// internal/application/scriptflow/.
//
// See docs/api-package-boundaries.md for the full architecture.
package script

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/generate"
)

// Handler is the HTTP transport for /api/script endpoints.
// Generation endpoints delegate to the application-layer GenerationService;
// remaining endpoints (curate, catalog, job status) delegate to the inner
// ScriptFlowHandler until extracted in future PRs.
type Handler struct {
	inner    *api.ScriptFlowHandler
	generate *generate.GenerationService
}

// NewHandler creates a new script Handler.
func NewHandler(inner *api.ScriptFlowHandler, gen *generate.GenerationService) *Handler {
	return &Handler{inner: inner, generate: gen}
}

// ── Generation endpoints (thin transport) ───────────────────────────────────

// GenerateFromClips handles POST /api/script/generate-from-clips.
func (h *Handler) GenerateFromClips(c *gin.Context) {
	if h.generate == nil {
		api.Error(c, http.StatusServiceUnavailable, "generation service not initialized")
		return
	}
	req, ok := api.BindJSON[api.GenerateFromClipsRequest](c)
	if !ok {
		return
	}
	result, err := h.generate.EnqueueFromClips(c.Request.Context(), mapFromClipsRequest(&req))
	if err != nil {
		api.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	api.OK(c, api.GenerateFromClipsResponse{
		OK:        result.OK,
		JobID:     result.JobID,
		Status:    result.Status,
		ClipCount: result.ClipCount,
	})
}

// GenerateWithImages handles POST /api/script/generate-with-images.
func (h *Handler) GenerateWithImages(c *gin.Context) {
	if h.generate == nil {
		api.Error(c, http.StatusServiceUnavailable, "generation service not initialized")
		return
	}
	req, ok := api.BindJSON[api.GenerateWithImagesRequest](c)
	if !ok {
		return
	}
	result, err := h.generate.EnqueueWithImages(c.Request.Context(), mapWithImagesRequest(&req))
	if err != nil {
		api.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	api.OK(c, api.GenerateFromClipsResponse{
		OK:        result.OK,
		JobID:     result.JobID,
		Status:    result.Status,
		ClipCount: result.ClipCount,
	})
}

// GenerateBatch handles POST /api/script/generate-batch.
// Async path delegates to GenerationService; sync path delegates to inner
// ScriptFlowHandler (to be extracted in PR 4).
func (h *Handler) GenerateBatch(c *gin.Context) {
	if h.inner == nil {
		api.Error(c, http.StatusServiceUnavailable, "script handler not initialized")
		return
	}
	// Delegate to inner ScriptFlowHandler for both async and sync paths.
	// PR 4 will extract this orchestration.
	h.inner.GenerateBatch(c)
}

// GetBatchProgress handles GET /api/script/generate-batch/progress.
func (h *Handler) GetBatchProgress(c *gin.Context) {
	if h.inner == nil {
		api.Error(c, http.StatusServiceUnavailable, "script handler not initialized")
		return
	}
	h.inner.GetBatchProgress(c)
}

// ── Route registration ─────────────────────────────────────────────────────

// RegisterRoutes registers /api/script routes.
// Generation endpoints run on the thin Handler; remaining endpoints delegate
// to the inner ScriptFlowHandler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	// Generation endpoints — thin transport
	r.POST("/generate-from-clips", h.GenerateFromClips)
	r.POST("/generate-with-images", h.GenerateWithImages)
	r.POST("/generate-batch", h.GenerateBatch)
	r.GET("/generate-batch/progress", h.GetBatchProgress)

	// Remaining endpoints — delegate to inner
	if h.inner != nil {
		h.inner.RegisterRoutesRemaining(r)
	}
}

// ── Request mapping helpers ─────────────────────────────────────────────────

func mapFromClipsRequest(req *api.GenerateFromClipsRequest) *generate.FromClipsCommand {
	return &generate.FromClipsCommand{
		Topic:                req.Topic,
		SourceText:           req.SourceText,
		Guidelines:           req.Guidelines,
		ClipIDs:              req.ClipIDs,
		NumClips:             req.NumClips,
		Title:                req.Title,
		OutputName:           req.OutputName,
		Language:             req.Language,
		Tone:                 req.Tone,
		Style:                req.Style,
		Model:                req.Model,
		DriveFolderID:        req.DriveFolderID,
		TargetWords:          req.TargetWords,
		Duration:             req.Duration,
		MinWords:             req.MinWords,
		SentencesPerImage:    req.SentencesPerImage,
		ImagesPerScene:       req.ImagesPerScene,
		ExtractEntities:      req.ExtractEntities,
		ArtlistSearch:        req.ArtlistSearch,
		StockSearch:          req.StockSearch,
		GenerateMetadata:     req.GenerateMetadata,
		GenerateVoiceover:    req.GenerateVoiceover,
		VoiceoverGroup:       req.VoiceoverGroup,
		VoiceoverFolderID:    req.VoiceoverFolderID,
		GenerateSceneImages:  req.GenerateSceneImages,
		Languages:            req.Languages,
		TranscriptPolicy:     req.TranscriptPolicy,
		OrderingStrategy:     req.OrderingStrategy,
		SaveToDB:             req.SaveToDB,
		GenerateTimeline:     req.GenerateTimeline,
		ForceRefresh:         req.ForceRefresh,
		MinQualityScore:      req.MinQualityScore,
		MinTranscriptWords:   req.MinTranscriptWords,
		PromptVersion:        req.PromptVersion,
		EditorPromptVersion:  req.EditorPromptVersion,
		QAPromptVersion:      req.QAPromptVersion,
	}
}

func mapWithImagesRequest(req *api.GenerateWithImagesRequest) *generate.WithImagesCommand {
	return &generate.WithImagesCommand{
		Topic:                req.Topic,
		SourceText:           req.SourceText,
		Guidelines:           req.Guidelines,
		ClipIDs:              req.ClipIDs,
		NumClips:             req.NumClips,
		Title:                req.Title,
		OutputName:           req.OutputName,
		Language:             req.Language,
		Tone:                 req.Tone,
		Style:                req.Style,
		Model:                req.Model,
		DriveFolderID:        req.DriveFolderID,
		TargetWords:          req.TargetWords,
		Duration:             req.Duration,
		MinWords:             req.MinWords,
		SentencesPerImage:    req.SentencesPerImage,
		ImagesPerScene:       req.ImagesPerScene,
		ArtlistSearch:        req.ArtlistSearch,
		StockSearch:          req.StockSearch,
		GenerateVoiceover:    req.GenerateVoiceover,
		VoiceoverGroup:       req.VoiceoverGroup,
		VoiceoverFolderID:    req.VoiceoverFolderID,
		Languages:            req.Languages,
		TranscriptPolicy:     req.TranscriptPolicy,
		OrderingStrategy:     req.OrderingStrategy,
		SaveToDB:             req.SaveToDB,
		GenerateTimeline:     req.GenerateTimeline,
		ForceRefresh:         req.ForceRefresh,
		MinQualityScore:      req.MinQualityScore,
		MinTranscriptWords:   req.MinTranscriptWords,
		PromptVersion:        req.PromptVersion,
		EditorPromptVersion:  req.EditorPromptVersion,
		QAPromptVersion:      req.QAPromptVersion,
	}
}
