package lessons

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/jobs"
	lessonsService "velox/go-master/internal/media/lessons"
	"velox/go-master/internal/media/models"
	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/handlerutil"
	"velox/go-master/pkg/textutil"
)

// Handler exposes lesson generation endpoints.
type Handler struct {
	svc     *lessonsService.Service
	jobsSvc *jobs.Service
	log     *zap.Logger
}

// NewHandler creates a new lessons handler.
func NewHandler(svc *lessonsService.Service, jobsSvc *jobs.Service, log *zap.Logger) *Handler {
	return &Handler{
		svc:     svc,
		jobsSvc: jobsSvc,
		log:     log,
	}
}

// RegisterRoutes registers /api/lessons routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.GenerateLesson)
	r.GET("/jobs", h.ListJobs)
}

// GenerateLessonRequest is the input for lesson generation.
type GenerateLessonRequest struct {
	SourceText     string `json:"source_text"`               // Required: full source text to process
	Title          string `json:"title,omitempty"`           // Lesson title (auto-extracted if empty)
	Language       string `json:"language,omitempty"`        // Output language (default: "it")
	Tone           string `json:"tone,omitempty"`            // Narrative tone (default: "educational")
	Model          string `json:"model,omitempty"`           // Ollama model (default: "gemma4:e4b")
	MaxChapters    int    `json:"max_chapters,omitempty"`    // Max chapters (0 = auto)
	GenerateImages bool   `json:"generate_images,omitempty"` // Generate AI images per chapter
	ImageStyle     string `json:"image_style,omitempty"`     // Image style
	ImageModel     string `json:"image_model,omitempty"`     // Image model (default: "flux-1-dev")
	ImageWidth     int    `json:"image_width,omitempty"`     // Image width
	ImageHeight    int    `json:"image_height,omitempty"`    // Image height
	GeneratePDF    bool   `json:"generate_pdf,omitempty"`    // Generate PDF output
	OllamaURL      string `json:"ollama_url,omitempty"`      // Ollama URL override
	Async          bool   `json:"async,omitempty"`           // Run as background job
}

// GenerateLesson handles POST /api/lessons/generate.
// Processes source text into a structured lesson with chapters, images, and PDF.
func (h *Handler) GenerateLesson(c *gin.Context) {
	if !handlerutil.RequireService(c, h.svc, "lessons service") {
		return
	}

	req, ok := apiutil.BindJSON[GenerateLessonRequest](c)
	if !ok {
		return
	}

	if req.SourceText == "" {
		apiutil.BadRequest(c, "source_text is required")
		return
	}

	// Async mode: enqueue background job
	if req.Async {
		if !handlerutil.RequireService(c, h.jobsSvc, "job system") {
			return
		}
		h.log.Info("enqueuing async lesson generate job", zap.String("title", req.Title))
		handlerutil.EnqueueAsync(c, h.jobsSvc, &handlerutil.EnqueueInput{
			Type: models.JobTypeLessonsProcess,
			Payload: map[string]any{
				"source_text":     req.SourceText,
				"title":           req.Title,
				"language":        req.Language,
				"tone":            req.Tone,
				"model":           req.Model,
				"max_chapters":    req.MaxChapters,
				"generate_images": req.GenerateImages,
				"image_style":     req.ImageStyle,
				"image_model":     req.ImageModel,
				"image_width":     req.ImageWidth,
				"image_height":    req.ImageHeight,
				"generate_pdf":    req.GeneratePDF,
				"ollama_url":      req.OllamaURL,
			},
			Priority:  5,
			ActiveKey: "lessons_generate_" + textutil.Truncate(req.Title, 50),
		}, "Lesson generation enqueued.")
		return
	}

	// Sync mode: process with timeout
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Minute)
	defer cancel()

	h.log.Info("generating lesson synchronously",
		zap.String("title", req.Title),
		zap.Int("source_len", len(req.SourceText)),
		zap.Int("max_chapters", req.MaxChapters),
		zap.Bool("generate_images", req.GenerateImages),
		zap.Bool("generate_pdf", req.GeneratePDF),
	)

	result, err := h.svc.GenerateLesson(ctx, &lessonsService.LessonRequest{
		SourceText:     req.SourceText,
		Title:          req.Title,
		Language:       req.Language,
		Tone:           req.Tone,
		Model:          req.Model,
		MaxChapters:    req.MaxChapters,
		GenerateImages: req.GenerateImages,
		ImageStyle:     req.ImageStyle,
		ImageModel:     req.ImageModel,
		ImageWidth:     req.ImageWidth,
		ImageHeight:    req.ImageHeight,
		GeneratePDF:    req.GeneratePDF,
		OllamaURL:      req.OllamaURL,
	})
	if err != nil {
		h.log.Error("lesson generation failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	if !result.Success {
		apiutil.Error(c, http.StatusInternalServerError, result.Error)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":            true,
		"success":       true,
		"title":         result.Title,
		"language":      result.Language,
		"chapters":      result.Chapters,
		"total_words":   result.TotalWords,
		"markdown_path": result.MarkdownPath,
		"pdf_path":      result.PDFPath,
		"generated_at":  result.GeneratedAt,
	})
}

// ListJobs returns all lesson generation jobs.
// GET /api/lessons/jobs?status=queued&limit=20&offset=0
func (h *Handler) ListJobs(c *gin.Context) {
	if !handlerutil.RequireService(c, h.jobsSvc, "job system") {
		return
	}

	pag := handlerutil.ParsePagination(c, 20, 1000)
	jobType := models.JobTypeLessonsProcess

	filter := models.JobFilter{
		Type:   &jobType,
		Status: handlerutil.ParseJobStatusFilter(c),
		Limit:  pag.Limit,
		Offset: pag.Offset,
	}

	jobsList, err := h.jobsSvc.List(c.Request.Context(), filter)
	if err != nil {
		h.log.Error("failed to list lesson jobs", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	handlerutil.ListJobsResponse(c, handlerutil.BuildJobSummaries(jobsList))
}

// processAsync enqueues a background job for lesson generation.
