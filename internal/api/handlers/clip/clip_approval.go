// Package handlers provides HTTP API handlers for clip approval operations
package clip

import (
	"net/http"
	"strings"
	"time"

	"velox/go-master/internal/nvidia"
	"velox/go-master/internal/stockdb"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ClipApprovalHandler gestisce le approvazioni di clip
type ClipApprovalHandler struct {
	NvidiaClient *nvidia.Client
	StockDB      *stockdb.StockDB
	Logger       *zap.Logger
}

// NewClipApprovalHandler crea un nuovo handler
func NewClipApprovalHandler(nvClient *nvidia.Client, sdb *stockdb.StockDB, logger *zap.Logger) *ClipApprovalHandler {
	return &ClipApprovalHandler{
		NvidiaClient: nvClient,
		StockDB:      sdb,
		Logger:       logger,
	}
}

// ============================================================================
// REQUEST/RESPONSE TYPES
// ============================================================================

// ReviewClipRequest richiedi verifica AI per una clip
type ReviewClipRequest struct {
	SceneText       string   `json:"scene_text" binding:"required"`
	SceneKeywords   string   `json:"scene_keywords" binding:"required"`
	VideoTitle      string   `json:"video_title" binding:"required"`
	VideoDescription string  `json:"video_description"`
	VideoURL        string   `json:"video_url"`
	ClipID          string   `json:"clip_id"`
}

// ReviewClipResponse risposta verifica AI
type ReviewClipResponse struct {
	ClipID          string   `json:"clip_id"`
	VideoTitle      string   `json:"video_title"`
	VideoURL        string   `json:"video_url"`
	RelevanceScore  int      `json:"relevance_score"`
	Recommendation  string   `json:"recommendation"` // "download", "review", "reject"
	Reason          string   `json:"reason"`
	MatchKeywords   []string `json:"match_keywords"`
	Warning         string   `json:"warning,omitempty"`
}

// BatchReviewRequest verifica batch di clip
type BatchReviewRequest struct {
	SceneText     string                    `json:"scene_text" binding:"required"`
	SceneKeywords string                    `json:"scene_keywords" binding:"required"`
	Videos        []nvidia.VideoCandidate   `json:"videos" binding:"required"`
}

// BatchReviewResponse risposta batch
type BatchReviewResponse struct {
	TotalVideos  int                       `json:"total_videos"`
	Approved     int                       `json:"approved"`
	NeedReview   int                       `json:"need_review"`
	Rejected     int                       `json:"rejected"`
	Results      []*nvidia.VerificationResult `json:"results"`
}

// ApproveClipRequest approva manualmente una clip
type ApproveClipRequest struct {
	ClipID    string `json:"clip_id" binding:"required"`
	Source    string `json:"source" binding:"required"` // drive, artlist, youtube, tiktok
	Approved  bool   `json:"approved"`
	Notes     string `json:"notes"`
}

// GetPendingClipsResponse response per clip in attesa
type GetPendingClipsResponse struct {
	TotalPending int                    `json:"total_pending"`
	Clips        []PendingClipItem      `json:"clips"`
}

// PendingClipItem clip in attesa di approvazione
type PendingClipItem struct {
	ClipID        string  `json:"clip_id"`
	Source        string  `json:"source"`
	SceneNumber   int     `json:"scene_number"`
	SceneText     string  `json:"scene_text"`
	VideoTitle    string  `json:"video_title,omitempty"`
	ThumbnailURL  string  `json:"thumbnail_url,omitempty"`
	RelevanceScore float64 `json:"relevance_score"`
	URL           string  `json:"url,omitempty"`
}

// ============================================================================
// HANDLERS
// ============================================================================

// ReviewClip godoc
// @Summary Verifica pertinenza clip YouTube con AI
// @Description Usa NVIDIA AI per verificare se un video YouTube è pertinente alla scena
// @Tags Clip Approval
// @Accept json
// @Produce json
// @Param request body ReviewClipRequest true "Clip review request"
// @Success 200 {object} ReviewClipResponse
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/clip/review [post]
func (h *ClipApprovalHandler) ReviewClip(c *gin.Context) {
	var req ReviewClipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Chiama NVIDIA AI per verifica
	result, err := h.NvidiaClient.VerifyYouTubeTitle(
		c.Request.Context(),
		req.SceneText,
		req.SceneKeywords,
		req.VideoTitle,
		req.VideoDescription,
	)
	if err != nil {
		h.Logger.Error("NVIDIA verification failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := ReviewClipResponse{
		ClipID:         req.ClipID,
		VideoTitle:     req.VideoTitle,
		VideoURL:       req.VideoURL,
		RelevanceScore: result.RelevanceScore,
		Recommendation: result.Recommendation,
		Reason:         result.Reason,
		MatchKeywords:  result.MatchKeywords,
		Warning:        result.Warning,
	}

	c.JSON(http.StatusOK, resp)
}

// BatchReviewClips godoc
// @Summary Verifica batch di clip YouTube con AI
// @Description Verifica multiple video YouTube in una sola chiamata
// @Tags Clip Approval
// @Accept json
// @Produce json
// @Param request body BatchReviewRequest true "Batch review request"
// @Success 200 {object} BatchReviewResponse
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/clip/batch-review [post]
func (h *ClipApprovalHandler) BatchReviewClips(c *gin.Context) {
	var req BatchReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.Videos) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "videos array cannot be empty"})
		return
	}

	// Chiama NVIDIA AI per verifica batch
	results, err := h.NvidiaClient.VerifyBatchTitles(
		c.Request.Context(),
		req.SceneText,
		req.SceneKeywords,
		req.Videos,
	)
	if err != nil {
		h.Logger.Error("NVIDIA batch verification failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Conta risultati
	approved := 0
	needReview := 0
	rejected := 0

	for _, r := range results {
		switch r.Recommendation {
		case "download":
			approved++
		case "review":
			needReview++
		case "reject":
			rejected++
		}
	}

	resp := BatchReviewResponse{
		TotalVideos: len(results),
		Approved:    approved,
		NeedReview:  needReview,
		Rejected:    rejected,
		Results:     results,
	}

	c.JSON(http.StatusOK, resp)
}

// ApproveClip godoc
// @Summary Approva o rifiuta manualmente una clip
// @Description Approvazione manuale di una clip trovata dal mapper
// @Tags Clip Approval
// @Accept json
// @Produce json
// @Param request body ApproveClipRequest true "Approve request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /api/clip/approve [post]
func (h *ClipApprovalHandler) ApproveClip(c *gin.Context) {
	var req ApproveClipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if h.StockDB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "StockDB not available"})
		return
	}

	// Fetch all clips and find the one matching clip_id
	clips, err := h.StockDB.GetAllClips()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read clips"})
		return
	}

	var found bool
	for i, clip := range clips {
		if clip.ClipID == req.ClipID {
			// Update tags to include approval status
			tags := clip.Tags
			if req.Approved {
				tags = append(tags, "approved")
			} else {
				tags = append(tags, "rejected")
			}
			clips[i].Tags = tags
			if err := h.StockDB.UpsertClip(clip); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save approval"})
				return
			}
			found = true
			break
		}
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "clip not found"})
		return
	}

	h.Logger.Info("Clip approval recorded",
		zap.String("clip_id", req.ClipID),
		zap.Bool("approved", req.Approved),
		zap.String("notes", req.Notes),
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"clip_id":  req.ClipID,
		"approved": req.Approved,
		"message":  "Clip approval recorded",
	})
}

// GetPendingClips godoc
// @Summary Ottieni clip in attesa di approvazione
// @Description Lista di clip che richiedono approvazione manuale
// @Tags Clip Approval
// @Produce json
// @Success 200 {object} GetPendingClipsResponse
// @Router /api/clip/pending [get]
func (h *ClipApprovalHandler) GetPendingClips(c *gin.Context) {
	if h.StockDB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "StockDB not available"})
		return
	}

	clips, err := h.StockDB.GetAllClips()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read clips"})
		return
	}

	var pending []PendingClipItem
	for _, clip := range clips {
		hasApproval := false
		for _, t := range clip.Tags {
			t = strings.TrimSpace(t)
			if t == "approved" || t == "rejected" {
				hasApproval = true
				break
			}
		}
		if !hasApproval {
			pending = append(pending, PendingClipItem{
				ClipID:   clip.ClipID,
				Source:   clip.Source,
				VideoTitle: clip.Filename,
			})
		}
	}

	resp := GetPendingClipsResponse{
		TotalPending: len(pending),
		Clips:        pending,
	}

	c.JSON(http.StatusOK, resp)
}

// GetClipSuggestions godoc
// @Summary Ottieni suggerimenti clip per una scena
// @Description Trova clip pertinenti per una scena specifica dello script
// @Tags Clip Approval
// @Accept json
// @Produce json
// @Param scene_number query int true "Scene number"
// @Param script_id query string true "Script ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/clip/suggestions [get]
func (h *ClipApprovalHandler) GetClipSuggestions(c *gin.Context) {
	sceneNum := c.Query("scene_number")
	scriptID := c.Query("script_id")

	h.Logger.Info("Getting clip suggestions",
		zap.String("script_id", scriptID),
		zap.String("scene_number", sceneNum),
	)

	if h.StockDB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "StockDB not available"})
		return
	}

	// Return all available clips as suggestions
	clips, err := h.StockDB.GetAllClips()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read clips"})
		return
	}

	type suggestion struct {
		ClipID   string `json:"clip_id"`
		Filename string `json:"filename"`
		Source   string `json:"source"`
		Tags     string `json:"tags"`
		Duration int    `json:"duration"`
	}

	var suggestions []suggestion
	for _, clip := range clips {
		suggestions = append(suggestions, suggestion{
			ClipID:   clip.ClipID,
			Filename: clip.Filename,
			Source:   clip.Source,
			Tags:     strings.Join(clip.Tags, ","),
			Duration: clip.Duration,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"script_id":    scriptID,
		"scene_number": sceneNum,
		"suggestions":  suggestions,
		"total":        len(suggestions),
	})
}

// NvidiaHealth godoc
// @Summary Verifica disponibilità API NVIDIA
// @Description Check se NVIDIA AI API è operativa
// @Tags Clip Approval
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/nvidia/health [get]
func (h *ClipApprovalHandler) NvidiaHealth(c *gin.Context) {
	err := h.NvidiaClient.CheckHealth(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "unhealthy",
			"error":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"api":    "nvidia",
		"model":  "z-ai/glm5",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// RegisterRoutes is retained for compatibility but no longer mounts HTTP routes.
func (h *ClipApprovalHandler) RegisterRoutes(_ *gin.RouterGroup) {}
