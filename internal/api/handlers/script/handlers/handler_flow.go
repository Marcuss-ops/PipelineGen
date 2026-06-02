package handlers

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
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

	// Generate video metadata for all requested languages (including base language)
	languages := []string{"en"} // Always include English for YouTube
	languageSet := map[string]bool{"en": true}

	// Add base language if not English
	if req.Language != "" && req.Language != "en" && !languageSet[req.Language] {
		languages = append(languages, req.Language)
		languageSet[req.Language] = true
	}

	// Add additional requested languages
	for _, lang := range req.Languages {
		if !languageSet[lang] {
			languages = append(languages, lang)
			languageSet[lang] = true
		}
	}

	// Generate metadata for all languages in parallel
	var mu sync.Mutex
	metadata := make([]VideoMetadata, 0, len(languages))
	var wg sync.WaitGroup

	for _, lang := range languages {
		wg.Add(1)
		go func(lang string) {
			defer wg.Done()

			meta := VideoMetadata{Language: lang}

			// Translate title to target language
			titleTranslated, _ := h.generator.TranslateText(ctx, req.Title, lang)
			if titleTranslated != "" {
				meta.Title = titleTranslated
			} else {
				meta.Title = req.Title
			}

			// Generate description and tags in English, or translate if not English
			if lang == "en" {
				if desc, tags, err := h.generator.GenerateVideoMetadata(ctx, req.Title); err == nil {
					meta.Description = desc
					meta.Tags = tags
				}
			} else {
				// Translate English metadata to target language
				if desc, tags, err := h.generator.GenerateVideoMetadata(ctx, req.Title); err == nil {
					descTranslated, _ := h.generator.TranslateText(ctx, desc, lang)
					if descTranslated != "" {
						meta.Description = descTranslated
					} else {
						meta.Description = desc
					}
					// Translate tags
					var translatedTags []string
					for _, tag := range tags {
						if t, err := h.generator.TranslateText(ctx, tag, lang); err == nil && t != "" {
							translatedTags = append(translatedTags, t)
						} else {
							translatedTags = append(translatedTags, tag)
						}
					}
					meta.Tags = translatedTags
				}
			}

			mu.Lock()
			metadata = append(metadata, meta)
			mu.Unlock()
		}(lang)
	}
	wg.Wait()

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