package handlers

import (
	"context"
	"fmt"
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
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/realtime"
	"velox/go-master/internal/ml/ollama"
	ollamatypes "velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/media/voiceover"
	"velox/go-master/internal/pkg/apiutil"
	"velox/go-master/internal/upload/drive"
)

// ScriptFlowHandler exposes text-only generation and image-from-text visualization.
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
	r.POST("/generate-from-source", h.GenerateFromSource)
	r.POST("/from-source", h.GenerateFromSource)
	r.POST("/visualize", h.Visualize)
	r.GET("/jobs/:job_id", h.GetJobStatus)
}

// GenerateTextRequest is the input for the text-only generation endpoint.
type GenerateTextRequest struct {
	Topic      string `json:"topic" binding:"required"`
	SourceText string `json:"source_text,omitempty"`
	Title      string `json:"title,omitempty"`
	Language   string `json:"language,omitempty"`
	Tone       string `json:"tone,omitempty"`
	Duration   int    `json:"duration,omitempty"`
	Model      string `json:"model,omitempty"`
}

// VisualizeRequest is the input for the text-to-image planning endpoint.
type VisualizeRequest struct {
	ScriptText      string  `json:"script_text" binding:"required"`
	Topic           string  `json:"topic,omitempty"`
	Style           string  `json:"style,omitempty"`
	Language        string  `json:"language,omitempty"`
	Model           string  `json:"model,omitempty"`
	Width           int     `json:"width,omitempty"`
	Height          int     `json:"height,omitempty"`
	MinScore        float64 `json:"min_score,omitempty"`
	MaxSegments     int     `json:"max_segments,omitempty"`
	GenerateMissing *bool   `json:"generate_missing,omitempty"`
}

// VisualAssetResult is returned for both reused and generated images.
type VisualAssetResult struct {
	ID          string   `json:"id,omitempty"`
	Hash        string   `json:"hash,omitempty"`
	Name        string   `json:"name,omitempty"`
	Source      string   `json:"source,omitempty"`
	Category    string   `json:"category,omitempty"`
	MediaType   string   `json:"media_type,omitempty"`
	Score       float64  `json:"score,omitempty"`
	LocalPath   string   `json:"local_path,omitempty"`
	PathRel     string   `json:"path_rel,omitempty"`
	SourceURL   string   `json:"source_url,omitempty"`
	DriveLink   string   `json:"drive_link,omitempty"`
	DriveFileID string   `json:"drive_file_id,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// VisualizeSegment is one sentence/beat from the script.
type VisualizeSegment struct {
	Index    int                  `json:"index"`
	Sentence string               `json:"sentence"`
	Query    string               `json:"query"`
	Action   string               `json:"action"` // reuse | generated | skipped
	Match    *realtime.MatchAsset `json:"match,omitempty"`
	Image    *VisualAssetResult   `json:"image,omitempty"`
	Error    string               `json:"error,omitempty"`
}

// GenerateText returns plain text only, with no entity extraction or asset linkage.
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

	// Generate video description and tags in English automatically based on the title
	videoDesc := ""
	videoTags := []string{}
	if metaDesc, metaTags, err := h.generator.GenerateVideoMetadata(ctx, req.Title); err != nil {
		h.log.Warn("Failed to generate video metadata", zap.Error(err))
	} else {
		videoDesc = metaDesc
		videoTags = metaTags
	}

	apiutil.OK(c, gin.H{
		"ok":            true,
		"topic":         req.Topic,
		"title":         req.Title,
		"script":        result.Script,
		"text":          result.Script,
		"word_count":    result.WordCount,
		"est_duration":  result.EstDuration,
		"model":         result.Model,
		"prompt":        result.Prompt,
		"video_desc_en": videoDesc,
		"video_tags":    videoTags,
	})
}

// Visualize turns script text into visual beats, reuses semantically matching assets, or generates new images.
func (h *ScriptFlowHandler) Visualize(c *gin.Context) {
	if h.imgService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "image service not initialized")
		return
	}

	req, ok := apiutil.BindJSON[VisualizeRequest](c)
	if !ok {
		return
	}
	req.ScriptText = strings.TrimSpace(req.ScriptText)
	if req.ScriptText == "" {
		apiutil.BadRequest(c, "script_text is required")
		return
	}

	if req.Language == "" {
		req.Language = "it"
	}
	if req.Width <= 0 {
		req.Width = 1344
	}
	if req.Height <= 0 {
		req.Height = 768
	}
	if req.MinScore <= 0 {
		req.MinScore = 0.78
	}
	if req.MaxSegments <= 0 {
		req.MaxSegments = 8
	}
	generateMissing := true
	if req.GenerateMissing != nil {
		generateMissing = *req.GenerateMissing
	}

	sentences := splitScriptSentences(req.ScriptText)
	if len(sentences) > req.MaxSegments {
		sentences = sentences[:req.MaxSegments]
	}
	if len(sentences) == 0 {
		sentences = []string{req.ScriptText}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Minute)
	defer cancel()

	// ── Parallel segment processing ────────────────────────────
	type segResult struct {
		index   int
		segment VisualizeSegment
	}

	resChan := make(chan segResult, len(sentences))
	sem := make(chan struct{}, 9) // Up to 9 concurrent image generations
	var wg sync.WaitGroup
	var usedReuse, usedGeneration bool
	var mu sync.Mutex

	for idx, sentence := range sentences {
		wg.Add(1)
		go func(idx int, sentence string) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire concurrency token
			defer func() { <-sem }() // Release token

			query := buildVisualQuery(sentence, req.Topic, req.Style, req.Language)
			segment := VisualizeSegment{
				Index:    idx,
				Sentence: sentence,
				Query:    query,
				Action:   "skipped",
			}

			// Step 1: Try realtime match for existing assets
			if h.realtimeSvc != nil {
				matchResp, err := h.realtimeSvc.Match(ctx, &realtime.MatchRequest{
					Query:              query,
					Mode:               "visual",
					Limit:              1,
					MinScore:           req.MinScore,
					AllowBackgroundGen: false,
					MediaType:          "image",
				})
				if err == nil && matchResp != nil && strings.HasPrefix(matchResp.Status, "instant_match") && matchResp.Asset != nil {
					segment.Action = "reuse"
					segment.Match = matchResp.Asset
					segment.Image = &VisualAssetResult{
						ID:        matchResp.Asset.ID,
						Score:     matchResp.Asset.Score,
						Source:    matchResp.Asset.Source,
						Name:      matchResp.Asset.Name,
						Category:  matchResp.Asset.Category,
						MediaType: matchResp.Asset.MediaType,
						LocalPath: matchResp.Asset.LocalPath,
						DriveLink: matchResp.Asset.DriveLink,
					}
					mu.Lock()
					usedReuse = true
					mu.Unlock()
					resChan <- segResult{index: idx, segment: segment}
					return
				}
			}

			if !generateMissing {
				resChan <- segResult{index: idx, segment: segment}
				return
			}

			// Step 2: Generate new image (runs in parallel with other goroutines)
			generated, err := h.imgService.GenerateSmartImage(
				ctx,
				req.Topic,
				req.Topic,
				req.Style,
				[]string{sentence},
				nil,
				req.Width,
				req.Height,
				req.Model,
				false,
			)
			if err != nil {
				segment.Error = err.Error()
				resChan <- segResult{index: idx, segment: segment}
				return
			}

			segment.Action = "generated"
			segment.Image = imageAssetToResult(generated)
			mu.Lock()
			usedGeneration = true
			mu.Unlock()
			resChan <- segResult{index: idx, segment: segment}
		}(idx, sentence)
	}

	wg.Wait()
	close(resChan)

	// Collect results in order
	segments := make([]VisualizeSegment, len(sentences))
	for res := range resChan {
		segments[res.index] = res.segment
	}

	mode := "mixed"
	switch {
	case usedGeneration && !usedReuse:
		mode = "generated"
	case usedReuse && !usedGeneration:
		mode = "reused"
	}

	apiutil.OK(c, gin.H{
		"ok":            true,
		"mode":          mode,
		"topic":         req.Topic,
		"style":         req.Style,
		"language":      req.Language,
		"segments":      segments,
		"segment_count": len(segments),
	})
}

func imageAssetToResult(asset *models.ImageAsset) *VisualAssetResult {
	if asset == nil {
		return nil
	}
	return &VisualAssetResult{
		ID:          fmt.Sprintf("%d", asset.ID),
		Hash:        asset.Hash,
		Name:        asset.SubjectID,
		Source:      asset.SourceURL,
		MediaType:   "image",
		LocalPath:   asset.PathRel,
		PathRel:     asset.PathRel,
		SourceURL:   asset.SourceURL,
		DriveLink:   driveLinkFromImageAsset(asset),
		DriveFileID: asset.DriveFileID,
		Description: asset.Description,
		Tags:        asset.Tags,
	}
}

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
