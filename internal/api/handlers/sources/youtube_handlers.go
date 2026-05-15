package sources

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/models"
)

type YouTubeClipHandler struct {
	service *youtubeclip.Service
	log     *zap.Logger
	jobsSvc *jobservice.Service
}

func NewYouTubeClipHandler(service *youtubeclip.Service, log *zap.Logger, jobsSvc *jobservice.Service) *YouTubeClipHandler {
	return &YouTubeClipHandler{
		service: service,
		log:     log,
		jobsSvc: jobsSvc,
	}
}

func (h *YouTubeClipHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/extract", h.Extract)
	r.GET("/info", h.GetVideoInfo)
	folders := r.Group("/folders")
	{
		folders.GET("/:id", h.GetFolder)
		folders.GET("/:id/clips", h.GetFolderClips)
		folders.GET("/search", h.SearchFolders)
		folders.GET("", h.ListFolders)
	}
}

func (h *YouTubeClipHandler) GetVideoInfo(c *gin.Context) {
	url := c.Query("url")
	if url == "" {
		apiutil.BadRequest(c, "url parameter is required")
		return
	}

	metadata, err := h.service.GetVideoInfo(c.Request.Context(), url)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, metadata)
}

func (h *YouTubeClipHandler) GetFolder(c *gin.Context) {
	folderID := c.Param("id")
	if folderID == "" {
		apiutil.BadRequest(c, "folder id required")
		return
	}

	folder, err := h.service.GetFolder(c.Request.Context(), folderID)
	if err != nil {
		apiutil.NotFound(c, "folder not found")
		return
	}

	apiutil.OK(c, gin.H{"folder": folder})
}

func (h *YouTubeClipHandler) GetFolderClips(c *gin.Context) {
	folderID := c.Param("id")
	if folderID == "" {
		apiutil.BadRequest(c, "folder id required")
		return
	}

	clips, err := h.service.ListFolderClips(c.Request.Context(), folderID)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"folder_id": folderID, "clips": clips})
}

func (h *YouTubeClipHandler) SearchFolders(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		apiutil.BadRequest(c, "query parameter 'q' required")
		return
	}

	folders, err := h.service.SearchFolders(c.Request.Context(), q)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"query": q, "folders": folders})
}

func (h *YouTubeClipHandler) ListFolders(c *gin.Context) {
	source := c.Query("source")

	folders, err := h.service.ListFolders(c.Request.Context(), source)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"folders": folders})
}

func (h *YouTubeClipHandler) Extract(c *gin.Context) {
	req, ok := apiutil.BindJSON[youtubeclip.ExtractRequest](c)
	if !ok {
		return
	}

	// Enqueue a job for youtube_clip.extract
	if h.jobsSvc != nil {
		// Convert request to map[string]any for job payload
		payloadBytes, err := json.Marshal(req)
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to marshal request: %w", err))
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to unmarshal request: %w", err))
			return
		}

		job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type:    models.JobType("youtube_clip.extract"),
			Payload: payload,
		})
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to enqueue job: %w", err))
			return
		}

		// Return job ID immediately
		apiutil.OK(c, gin.H{
			"job_id":     job.ID,
			"message":    "YouTube clip extraction job enqueued",
			"status_url": "/api/jobs/" + job.ID + "/full",
		})
		return
	}

	apiutil.InternalError(c, fmt.Errorf("jobs service not available"))
}
