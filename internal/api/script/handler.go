// Package script provides the HTTP transport layer for script generation endpoints.
//
// This package contains the ScriptFlowHandler (business logic) and Handler
// (thin HTTP transport wrapper). All handler_script_handlers_*.go files were
// migrated from package api into this package.
package script

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/generate"
	"github.com/Marcuss-ops/PipelineGen/internal/contracts/scriptjobs"
)

// Handler is the HTTP transport for /api/script endpoints.
// Generation endpoints delegate to the application-layer GenerationService;
// remaining endpoints (curate, catalog, job status) delegate to the inner
// ScriptFlowHandler.
type Handler struct {
	inner    *ScriptFlowHandler
	generate GenerationService
}

// NewHandler creates a new script Handler.
// The gen parameter is the concrete *generate.GenerationService, which
// satisfies the GenerationService interface defined below.
func NewHandler(inner *ScriptFlowHandler, gen GenerationService) *Handler {
	return &Handler{inner: inner, generate: gen}
}

// GenerationService is a local interface adapter for the application-layer
// generation use case, decoupling the HTTP transport from the concrete
// package. This avoids an import cycle when commands.go is removed and
// the service accepts scriptjobs.GenerationSpec directly.
type GenerationService interface {
	EnqueueFromClips(ctx context.Context, spec scriptjobs.GenerationSpec) (*generate.FromClipsResult, error)
	EnqueueWithImages(ctx context.Context, spec scriptjobs.GenerationSpec) (*generate.FromClipsResult, error)
}

// ── Generation endpoints (thin transport) ───────────────────────────────────

// GenerateFromClips handles POST /api/script/generate-from-clips.
func (h *Handler) GenerateFromClips(c *gin.Context) {
	if h.generate == nil {
		api.Error(c, http.StatusServiceUnavailable, "generation service not initialized")
		return
	}
	req, ok := api.BindJSON[GenerateFromClipsRequest](c)
	if !ok {
		return
	}
	result, err := h.generate.EnqueueFromClips(c.Request.Context(), fromClipsRequestToSpec(&req))
	if err != nil {
		api.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	api.OK(c, GenerateFromClipsResponse{
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
	req, ok := api.BindJSON[GenerateWithImagesRequest](c)
	if !ok {
		return
	}
	result, err := h.generate.EnqueueWithImages(c.Request.Context(), withImagesRequestToSpec(&req))
	if err != nil {
		api.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	api.OK(c, GenerateFromClipsResponse{
		OK:        result.OK,
		JobID:     result.JobID,
		Status:    result.Status,
		ClipCount: result.ClipCount,
	})
}

// GenerateBatch handles POST /api/script/generate-batch.
func (h *Handler) GenerateBatch(c *gin.Context) {
	if h.inner == nil {
		api.Error(c, http.StatusServiceUnavailable, "script handler not initialized")
		return
	}
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
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate-from-clips", h.GenerateFromClips)
	r.POST("/generate-with-images", h.GenerateWithImages)
	r.POST("/generate-batch", h.GenerateBatch)
	r.GET("/generate-batch/progress", h.GetBatchProgress)

	if h.inner != nil {
		h.inner.RegisterRoutesRemaining(r)
	}
}

// ── Request mapping helpers ─────────────────────────────────────────────────

func fromClipsRequestToSpec(req *GenerateFromClipsRequest) scriptjobs.GenerationSpec {
	return scriptjobs.GenerationSpec{
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

func withImagesRequestToSpec(req *GenerateWithImagesRequest) scriptjobs.GenerationSpec {
	return scriptjobs.GenerationSpec{
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
