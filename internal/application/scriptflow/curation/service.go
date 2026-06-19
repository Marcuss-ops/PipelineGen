// Package curation implements clip source and curate endpoints.
//
// This package owns the HTTP handlers for catalog-first script generation
// (GenerateFromCatalog) and natural-language media curation (Curate). Both
// endpoints are async-only — they validate the request and enqueue a
// background job. The actual job processing logic lives in the api/
// job handlers (HandleCatalogScriptGenerateJob, HandleCurateJob) which
// use the ScriptFlowHandler's internal services.
package curation

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts"
)

// HTTP response helpers (avoid import cycle with internal/api/).
func badRequest(c *gin.Context, msg string)         { c.JSON(http.StatusBadRequest, gin.H{"error": msg}) }
func svcUnavailable(c *gin.Context, msg string)     { c.JSON(http.StatusServiceUnavailable, gin.H{"error": msg}) }
func internalError(c *gin.Context, err error)       { c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()}) }

// CurationService handles clip source and curate HTTP endpoints.
type CurationService struct {
	clipSourceBuilder *scripts.ClipSourceBuilder
	jobsSvc           *jobservice.Service
	log               *zap.Logger
}

// NewCurationService creates a new CurationService.
func NewCurationService(
	clipSourceBuilder *scripts.ClipSourceBuilder,
	jobsSvc *jobservice.Service,
	log *zap.Logger,
) *CurationService {
	return &CurationService{
		clipSourceBuilder: clipSourceBuilder,
		jobsSvc:           jobsSvc,
		log:               log,
	}
}

// SetClipSourceBuilder sets the clip source builder (late-binding).
func (s *CurationService) SetClipSourceBuilder(b *scripts.ClipSourceBuilder) {
	s.clipSourceBuilder = b
}

// GenerateFromCatalog is the async-only endpoint for catalog-first script generation.
// POST /api/script/generate-from-catalog
func (s *CurationService) GenerateFromCatalog(c *gin.Context) {
	var req GenerateFromCatalogRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request: "+err.Error())
		return
	}

	if req.Topic == "" {
		badRequest(c, "topic is required")
		return
	}
	if req.MaxClips <= 0 {
		req.MaxClips = 10
	}
	if req.MinCoverage <= 0 {
		req.MinCoverage = 0.3
	}

	if s.clipSourceBuilder == nil {
		svcUnavailable(c, "clip source builder not initialized")
		return
	}
	if s.jobsSvc == nil {
		svcUnavailable(c, "job service not initialized")
		return
	}

	selectedIDs, catalogReport, err := s.clipSourceBuilder.SelectClipsForTopic(c.Request.Context(), req.Topic, req.MaxClips)
	if err != nil {
		s.log.Error("catalog scan failed", zap.String("topic", req.Topic), zap.Error(err))
		internalError(c, err)
		return
	}

	if len(selectedIDs) == 0 {
		badRequest(c, "no usable clips found for topic: "+req.Topic)
		return
	}

	if catalogReport.CoverageScore < req.MinCoverage {
		badRequest(c, "insufficient catalog coverage for: "+req.Topic)
		return
	}

	payload := JobPayloadCatalogScript{
		ClipIDs:            selectedIDs,
		Title:              req.Title,
		OutputName:         req.OutputName,
		Language:           req.Language,
		Tone:               req.Tone,
		Model:              req.Model,
		TargetWords:        req.TargetWords,
		Duration:           req.Duration,
		TranscriptPolicy:   req.TranscriptPolicy,
		OrderingStrategy:   req.OrderingStrategy,
		CreateDoc:          req.CreateDoc,
		SaveToDB:           req.SaveToDB,
		ForceRefresh:       req.ForceRefresh,
		MinQualityScore:    req.MinQualityScore,
		MinTranscriptWords: req.MinTranscriptWords,
	}

	payloadBytes, _ := json.Marshal(payload)
	var payloadMap map[string]any
	json.Unmarshal(payloadBytes, &payloadMap)

	s.log.Info("enqueuing catalog script generation",
		zap.String("topic", req.Topic),
		zap.Int("clips_selected", len(selectedIDs)),
		zap.Float64("coverage", catalogReport.CoverageScore),
	)

	job, err := s.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:    "script.generate_from_catalog",
		Payload: payloadMap,
	})
	if err != nil {
		s.log.Error("failed to enqueue catalog script job", zap.Error(err))
		svcUnavailable(c, "failed to enqueue job")
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"ok":             true,
		"job_id":         job.ID,
		"status":         "queued",
		"catalog_report": catalogReport,
	})
}

// Curate is the async-only endpoint for media curation.
// POST /api/script/curate
func (s *CurationService) Curate(c *gin.Context) {
	if s.jobsSvc == nil {
		svcUnavailable(c, "jobs service not initialized")
		return
	}

	var req CurateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request: "+err.Error())
		return
	}

	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		badRequest(c, "query is required")
		return
	}

	// Apply defaults
	if req.Language == "" {
		req.Language = "en"
	}
	if req.Tone == "" {
		req.Tone = "comedy"
	}
	if req.MaxClips <= 0 {
		req.MaxClips = 10
	}
	if req.MaxClips > 30 {
		req.MaxClips = 30
	}
	if req.TargetWords <= 0 {
		req.TargetWords = 2000
	}
	if req.MinScore <= 0 {
		req.MinScore = 0.5
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = req.Query
		if len(title) > 80 {
			title = title[:80] + "..."
		}
	}
	req.Title = title

	payload := map[string]any{
		"query":               req.Query,
		"title":               req.Title,
		"language":            req.Language,
		"tone":                req.Tone,
		"model":               req.Model,
		"max_clips":           req.MaxClips,
		"target_words":        req.TargetWords,
		"max_chars_per_scene": req.MaxCharsPerScene,
		"min_score":           req.MinScore,
		"source":              req.Source,
		"media_type":          req.MediaType,
		"type":                req.Type,
		"style":               req.Style,
		"style_instructions":  req.StyleInstructions,
		"selectable_clips":    req.SelectableClips,
		"generate_voiceover":  req.GenerateVoiceover,
		"voiceover_group":     req.VoiceoverGroup,
		"voiceover_folder_id": req.VoiceoverFolderID,
		"force_refresh":       req.ForceRefresh,
	}

	s.log.Info("enqueuing script.curate job",
		zap.String("query", req.Query),
		zap.String("title", req.Title),
		zap.Int("max_clips", req.MaxClips),
	)

	job, err := s.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:       "script.curate",
		Payload:    payload,
		MaxRetries: 2,
	})
	if err != nil {
		s.log.Error("failed to enqueue curate job", zap.Error(err))
		internalError(c, err)
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"ok":        true,
		"job_id":    job.ID,
		"status":    string(job.Status),
		"query":     req.Query,
		"max_clips": req.MaxClips,
	})
}
