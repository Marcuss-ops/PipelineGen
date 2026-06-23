// Package register provides thin HTTP handlers for YouTube clip registration.
package register

import (
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

// BatchRegisterResponse is the response for batch registration.
type BatchRegisterResponse struct {
	OK        bool                        `json:"ok"`
	Total     int                         `json:"total"`
	Succeeded int                         `json:"succeeded"`
	Failed    int                         `json:"failed"`
	Results   []sourcing.BatchClipResult  `json:"results"`
}

// Handler manages YouTube clip registration. All business orchestration
// lives in sourcing.Service — the handler is pure transport.
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

// BatchRegisterFromYouTube handles POST /api/media/register-batch.
// Thin transport: delegates all orchestration to sourcing.Service.BatchRegisterFromYouTube.
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

	// Backfill folder_id from the request-level default
	for i := range req.Clips {
		if req.Clips[i].FolderID == "" && req.FolderID != "" {
			req.Clips[i].FolderID = req.FolderID
		}
	}

	// Build commands from request clips
	commands := make([]sourcing.RegisterClipCommand, len(req.Clips))
	for i, clipReq := range req.Clips {
		commands[i] = toRegisterClipCommand(clipReq)
	}

	result := h.svc.BatchRegisterFromYouTube(c.Request.Context(), commands)

	apiutil.OK(c, BatchRegisterResponse{
		OK:        result.OK,
		Total:     result.Total,
		Succeeded: result.Succeeded,
		Failed:    result.Failed,
		Results:   result.Results,
	})
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
