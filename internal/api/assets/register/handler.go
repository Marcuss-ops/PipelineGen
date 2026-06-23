// Package register provides thin HTTP handlers for YouTube clip registration.
package register

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// RegisterFromYouTubeRequest is the JSON body for registering a clip from a YouTube URL.
type RegisterFromYouTubeRequest struct {
	URL         string   `json:"url" binding:"required"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Source      string   `json:"source"`
	Category    string   `json:"category"`
	Group       string   `json:"group"`
	FolderID    string   `json:"folder_id"`
	Start       float64  `json:"start"`
	End         float64  `json:"end"`
	Force       bool     `json:"force"`
}

// BatchRegisterRequest is the JSON body for batch registering clips from YouTube.
type BatchRegisterRequest struct {
	FolderID string                       `json:"folder_id"`
	Clips    []RegisterFromYouTubeRequest `json:"clips" binding:"required"`
}

// BatchClipResult is the result for a single clip in a batch registration.
type BatchClipResult struct {
	ClipID    string `json:"clip_id,omitempty"`
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Duplicate bool   `json:"duplicate,omitempty"`
}

// BatchRegisterResponse is the response for batch registration.
type BatchRegisterResponse struct {
	OK        bool              `json:"ok"`
	Total     int               `json:"total"`
	Succeeded int               `json:"succeeded"`
	Failed    int               `json:"failed"`
	Results   []BatchClipResult `json:"results"`
}

// Handler manages YouTube clip registration (download + metadata + Drive + Qdrant).
type Handler struct {
	svc *sourcing.Service
	log *zap.Logger
}

// NewHandler creates a YouTube registration handler.
func NewHandler(svc *sourcing.Service, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes registers the registration endpoints.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/register-from-youtube", h.RegisterFromYouTube)
	r.POST("/register-batch", h.BatchRegisterFromYouTube)
}

// RegisterFromYouTube handles POST /api/media/register-from-youtube.
func (h *Handler) RegisterFromYouTube(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "register service not wired")
		return
	}

	req, ok := apiutil.BindJSON[RegisterFromYouTubeRequest](c)
	if !ok {
		return
	}

	res, err := h.svc.RegisterFromYouTube(c.Request.Context(), toRegisterClipCommand(req))
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	if res == nil {
		apiutil.Error(c, http.StatusInternalServerError, "empty registration result")
		return
	}

	apiutil.OK(c, gin.H{
		"ok":              res.OK,
		"duplicate":       res.Duplicate,
		"clip_id":         res.ClipID,
		"video_id":        res.VideoID,
		"name":            res.Name,
		"filename":        res.Filename,
		"duration_sec":    res.DurationSec,
		"drive_link":      res.DriveLink,
		"drive_file_id":   res.DriveFileID,
		"file_hash":       res.FileHash,
		"source":          res.Source,
		"category":        res.Category,
		"tags":            res.Tags,
		"local_path":      res.LocalPath,
		"indexed":         res.Indexed,
		"indexing_status": res.IndexingStatus,
		"transcribed":     res.Transcribed,
		"language":        res.Language,
		"related_clips":   res.RelatedClips,
		"message":         res.Message,
	})
}

// BatchRegisterFromYouTube handles POST /api/media/register-batch
func (h *Handler) BatchRegisterFromYouTube(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "register service not wired")
		return
	}

	var req BatchRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}
	if len(req.Clips) == 0 {
		apiutil.BadRequest(c, "clips list is empty")
		return
	}

	for i := range req.Clips {
		if req.Clips[i].FolderID == "" && req.FolderID != "" {
			req.Clips[i].FolderID = req.FolderID
		}
	}

	ctx := c.Request.Context()
	log := h.log.With(zap.String("handler", "batch-register"), zap.Int("total", len(req.Clips)))

	results := make([]BatchClipResult, len(req.Clips))
	var succeeded, failed int

	log.Info("starting batch registration", zap.Int("clips", len(req.Clips)))
	for i, clipReq := range req.Clips {
		result := h.processBatchClip(ctx, clipReq)
		results[i] = result
		if result.OK || result.Duplicate {
			succeeded++
		} else {
			failed++
		}
		log.Info("batch clip processed",
			zap.Int("index", i+1),
			zap.Int("total", len(req.Clips)),
			zap.String("name", clipReq.Name),
			zap.Bool("ok", result.OK),
			zap.Bool("duplicate", result.Duplicate),
			zap.String("error", result.Error),
		)
	}

	log.Info("batch registration completed", zap.Int("succeeded", succeeded), zap.Int("failed", failed))
	apiutil.OK(c, BatchRegisterResponse{
		OK:        true,
		Total:     len(req.Clips),
		Succeeded: succeeded,
		Failed:    failed,
		Results:   results,
	})
}

func (h *Handler) processBatchClip(ctx context.Context, clipReq RegisterFromYouTubeRequest) BatchClipResult {
	result := BatchClipResult{Name: clipReq.Name}
	if h.svc == nil {
		result.Error = "register service not wired"
		return result
	}

	res, err := h.svc.RegisterFromYouTube(ctx, toRegisterClipCommand(clipReq))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if res == nil {
		result.Error = "empty registration result"
		return result
	}
	result.OK = res.OK
	result.ClipID = res.ClipID
	result.Duplicate = res.Duplicate
	if res.Duplicate {
		result.OK = false
	}
	if !res.OK && res.Message != "" {
		result.Error = res.Message
	}
	return result
}

func toRegisterClipCommand(req RegisterFromYouTubeRequest) sourcing.RegisterClipCommand {
	return sourcing.RegisterClipCommand{
		URL:         strings.TrimSpace(req.URL),
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Tags:        append([]string(nil), req.Tags...),
		Source:      strings.TrimSpace(req.Source),
		Category:    strings.TrimSpace(req.Category),
		Group:       strings.TrimSpace(req.Group),
		FolderID:    strings.TrimSpace(req.FolderID),
		StartSec:    req.Start,
		EndSec:      req.End,
		Force:       req.Force,
	}
}
