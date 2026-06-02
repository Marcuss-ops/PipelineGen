package handlers

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/config"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/images"
	"velox/go-master/internal/media/realtime"
	"velox/go-master/internal/ml/ollama"
	ollamatypes "velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/media/voiceover"
	"velox/go-master/internal/pkg/apiutil"
	"velox/go-master/internal/upload/drive"
)

// ScriptFlowHandler exposes script generation endpoints.
// Provides text-only generation and generation with images, both with full metadata.
type ScriptFlowHandler struct {
	generator   *ollama.Generator
	imgService  *images.Service
	realtimeSvc *realtime.Service
	voService   *voiceover.Service
	docClient   drive.DocClient
	jobsSvc     *jobservice.Service
	cfg         *config.Config
	log         *zap.Logger
}

// NewScriptFlowHandler creates the handler for text and visual flows.
func NewScriptFlowHandler(gen *ollama.Generator, imgSvc *images.Service, realtimeSvc *realtime.Service, voSvc *voiceover.Service, docClient drive.DocClient, jobsSvc *jobservice.Service, cfg *config.Config, log *zap.Logger) *ScriptFlowHandler {
	return &ScriptFlowHandler{
		generator:   gen,
		imgService:  imgSvc,
		realtimeSvc: realtimeSvc,
		voService:   voSvc,
		docClient:   docClient,
		jobsSvc:     jobsSvc,
		cfg:         cfg,
		log:         log,
	}
}

// RegisterRoutes registers /api/script routes.
func (h *ScriptFlowHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.GenerateText)
	r.POST("/text", h.GenerateText)
	r.POST("/generate-with-images", h.GenerateFromSource)
	r.POST("/from-source", h.GenerateFromSource) // alias for backwards compat
	r.GET("/jobs/:job_id", h.GetJobStatus)
}

// VideoMetadata contains YouTube metadata for multiple languages
type VideoMetadata struct {
	Language    string   `json:"language"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// GenerateTextRequest is the input for the text-only generation endpoint.
type GenerateTextRequest struct {
	Topic      string   `json:"topic" binding:"required"`
	SourceText string   `json:"source_text,omitempty"`
	Title      string   `json:"title,omitempty"`
	Language   string   `json:"language,omitempty"`
	Tone       string   `json:"tone,omitempty"`
	Duration   int      `json:"duration,omitempty"`
	Model      string   `json:"model,omitempty"`
	Languages  []string `json:"languages,omitempty"` // Additional languages for metadata translation
}

// GenerateText returns plain text only, with no entity extraction or asset linkage.
// Also generates YouTube metadata (description, tags, translated titles) for multiple languages.
func (h *ScriptFlowHandler) GenerateText(c *gin.Context) {
	if h.generator == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "script generator not initialized")
		return
	}

	req, ok := apiutil.BindJSON[GenerateTextRequest](c)
	if !ok {
		return
	}
	req.Topic = strings.TrimSpace(req.Topic)
	if req.Topic == "" {
		apiutil.BadRequest(c, "topic is required")
		return
	}

	if req.Language == "" {
		req.Language = "it"
	}
	if req.Tone == "" {
		req.Tone = "documentary"
	}
	if req.Duration <= 0 {
		req.Duration = 60
	}
	if req.Title == "" {
		req.Title = req.Topic
	}
	if req.SourceText == "" {
		req.SourceText = req.Topic
	}
	if req.Model == "" && h.cfg != nil {
		req.Model = h.cfg.External.OllamaModel
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	result, err := h.generator.GenerateScript(ctx, ollamatypes.TextGenerationRequest{
		Language:   req.Language,
		Duration:   req.Duration,
		Tone:       req.Tone,
		Model:      req.Model,
		Prompt:     req.Topic,
		SourceText: req.SourceText,
		Title:      req.Title,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// Generate video metadata for all requested languages
	languages := BuildMetadataLanguages(req.Language, req.Languages)
	metadata := GenerateVideoMetadata(ctx, h.generator, req.Title, languages)

	apiutil.OK(c, gin.H{
		"ok":           true,
		"topic":        req.Topic,
		"title":        req.Title,
		"script":       result.Script,
		"text":         result.Script,
		"word_count":   result.WordCount,
		"est_duration": result.EstDuration,
		"model":        result.Model,
		"prompt":       result.Prompt,
		"metadata":     metadata,
	})
}

// splitScriptSentences splits script text into sentences for scene generation.
func splitScriptSentences(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	re := regexp.MustCompile(`(?m)([^.!?]+[.!?]+|[^.!?]+$)`)
	parts := re.FindAllString(text, -1)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "•-* \t")
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func buildVisualQuery(sentence, topic, style, language string) string {
	parts := []string{strings.TrimSpace(sentence)}
	if t := strings.TrimSpace(topic); t != "" {
		parts = append(parts, t)
	}
	if s := strings.TrimSpace(style); s != "" {
		parts = append(parts, s)
	}
	if l := strings.TrimSpace(language); l != "" {
		parts = append(parts, l)
	}
	return strings.Join(parts, " | ")
}