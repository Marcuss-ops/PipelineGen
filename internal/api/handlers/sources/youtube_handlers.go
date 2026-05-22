package sources

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/apiutil"
	"velox/go-master/internal/sources/youtube"
)

type YouTubeClipHandler struct {
	service *youtube.Service
	log     *zap.Logger
	jobsSvc *jobservice.Service
}

func NewYouTubeClipHandler(service *youtube.Service, log *zap.Logger, jobsSvc *jobservice.Service) *YouTubeClipHandler {
	return &YouTubeClipHandler{
		service: service,
		log:     log,
		jobsSvc: jobsSvc,
	}
}

func (h *YouTubeClipHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/process", h.Extract)
	r.GET("/info", h.GetVideoInfo)
	r.GET("/search", h.SearchTopics)
	r.POST("/search", h.SearchTopics)
}

func (h *YouTubeClipHandler) SearchTopics(c *gin.Context) {
	var req youtube.TopicSearchRequest
	if err := c.ShouldBind(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	if req.Q == "" {
		apiutil.BadRequest(c, "q parameter is required")
		return
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}
	if req.Limit > 50 {
		req.Limit = 50
	}

	resp, err := h.service.SearchTopicVideos(c.Request.Context(), req.Q, req.Limit, req.Sort)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, resp)
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

func (h *YouTubeClipHandler) Extract(c *gin.Context) {
	req, ok := apiutil.BindJSON[youtube.ExtractRequest](c)
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
