package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/core/jobs"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
	"velox/go-master/pkg/apiutil"
)

// CurateRequest is the input for POST /api/script/curate.
// Accepts a natural language query to search for clips and generate a compilation.
type CurateRequest struct {
	// Query is the natural language topic (e.g. "funny actors parenting stories").
	Query string `json:"query" binding:"required"`

	// Title is an optional explicit title. Auto-generated from search results when empty.
	Title string `json:"title,omitempty"`

	Language string `json:"language,omitempty"`
	Tone     string `json:"tone,omitempty"`
	Model    string `json:"model,omitempty"`

	MaxClips         int     `json:"max_clips,omitempty"`
	TargetWords      int     `json:"target_words,omitempty"`
	MaxCharsPerScene int     `json:"max_chars_per_scene,omitempty"`
	MinScore         float64 `json:"min_score,omitempty"`

	Source    string `json:"source,omitempty"`
	MediaType string `json:"media_type,omitempty"`

	Type              string `json:"type,omitempty"`
	Style             string `json:"style,omitempty"`
	StyleInstructions string `json:"style_instructions,omitempty"`

	// SelectableClips controlla quante clip candidates cercare (il "pool" di clip
	// da cui Gemma può scegliere). Quando > 0, la ricerca cerca questo numero di clip
	// e le passa al narrative planner. MaxClips controlla quante clip
	// vengono effettivamente usate nello script finale.
	SelectableClips int `json:"selectable_clips,omitempty"`

	// GenerateVoiceover, se true, genera un file voiceover separato per ogni scena.
	GenerateVoiceover bool   `json:"generate_voiceover,omitempty"`
	VoiceoverGroup    string `json:"voiceover_group,omitempty"`
	// VoiceoverFolderID è l'ID della cartella Drive dove creare la subdirectory
	// con i file voiceover (es. cartella comedy). Se vuoto, usa il default.
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`

	ForceRefresh bool `json:"force_refresh,omitempty"`
}

// Curate is the async-only endpoint for media curation.
// POST /api/script/curate
//
// Unlike /generate-from-clips which requires explicit clip IDs, this endpoint
// accepts a NATURAL LANGUAGE QUERY, searches ALL available media via Qdrant
// semantic search, selects the best matches, and generates a complete
// compilation script with intro and outro.
func (h *ScriptFlowHandler) Curate(c *gin.Context) {
	if h.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}

	var req CurateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		apiutil.BadRequest(c, "query is required")
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
		// Truncate long queries for display
		if len(title) > 80 {
			title = title[:80] + "..."
		}
	}
	req.Title = title

	// Build payload for the job system
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

	h.log.Info("enqueuing script.curate job",
		zap.String("query", req.Query),
		zap.String("title", req.Title),
		zap.Int("max_clips", req.MaxClips),
	)

	job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:       models.JobType(jobs.JobTypeMediaCurate),
		Payload:    payload,
		MaxRetries: 2,
	})
	if err != nil {
		h.log.Error("failed to enqueue curate job", zap.Error(err))
		apiutil.InternalError(c, err)
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

// jobPayloadCurate is the runtime payload for script.curate job processing.
type jobPayloadCurate struct {
	Query             string  `json:"query"`
	Title             string  `json:"title"`
	Language          string  `json:"language"`
	Tone              string  `json:"tone"`
	Model             string  `json:"model"`
	MaxClips          int     `json:"max_clips"`
	TargetWords       int     `json:"target_words"`
	MaxCharsPerScene  int     `json:"max_chars_per_scene"`
	MinScore          float64 `json:"min_score"`
	Source            string  `json:"source"`
	MediaType         string  `json:"media_type"`
	Type              string  `json:"type"`
	Style             string  `json:"style"`
	StyleInstructions string  `json:"style_instructions"`
	SelectableClips   int     `json:"selectable_clips"`
	GenerateVoiceover bool    `json:"generate_voiceover"`
	VoiceoverGroup    string  `json:"voiceover_group"`
	VoiceoverFolderID string  `json:"voiceover_folder_id"`
	ForceRefresh      bool    `json:"force_refresh"`
}
