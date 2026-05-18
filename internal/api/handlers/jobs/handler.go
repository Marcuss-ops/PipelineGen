package jobs

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/jobs"
	"velox/go-master/internal/pkg/apiutil"
	"velox/go-master/internal/media/models"
)

type Handler struct {
	service *jobs.Service
	log     *zap.Logger
}

func NewHandler(service *jobs.Service, log *zap.Logger) *Handler {
	return &Handler{service: service, log: log}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("", h.Enqueue)
	r.GET("", h.List)
	r.GET("/:id", h.Get)
	r.GET("/:id/full", h.GetFull)
	r.POST("/:id/cancel", h.Cancel)
	r.POST("/:id/retry", h.Retry)
	r.GET("/:id/events", h.Events)
}

func (h *Handler) Enqueue(c *gin.Context) {
	req, ok := apiutil.BindJSON[jobs.EnqueueRequest](c)
	if !ok {
		return
	}

	job, err := h.service.Enqueue(c.Request.Context(), &req)
	if err != nil {
		h.log.Error("failed to enqueue job", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.Accepted(c, gin.H{
		"job_id": job.ID,
		"job": gin.H{
			"id":       job.ID,
			"type":     job.Type,
			"status":   job.Status,
			"project":  job.Project,
			"progress": job.Progress,
		},
	})
}

func (h *Handler) Get(c *gin.Context) {
	id := c.Param("id")

	job, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		apiutil.NotFound(c, "job not found")
		return
	}

	apiutil.OK(c, gin.H{"job": job})
}

func (h *Handler) List(c *gin.Context) {
	var filter models.JobFilter

	if status := c.Query("status"); status != "" {
		s := models.JobStatus(status)
		filter.Status = &s
	}
	if jobType := c.Query("type"); jobType != "" {
		t := models.JobType(jobType)
		filter.Type = &t
	}
	if workerID := c.Query("worker_id"); workerID != "" {
		filter.WorkerID = workerID
	}
	if limit := c.Query("limit"); limit != "" {
		filter.Limit, _ = strconv.Atoi(limit)
	}
	if offset := c.Query("offset"); offset != "" {
		filter.Offset, _ = strconv.Atoi(offset)
	}

	jobs, err := h.service.List(c.Request.Context(), filter)
	if err != nil {
		h.log.Error("failed to list jobs", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"jobs": jobs, "count": len(jobs)})
}

func (h *Handler) Cancel(c *gin.Context) {
	id := c.Param("id")

	if err := h.service.Cancel(c.Request.Context(), id); err != nil {
		h.log.Error("failed to cancel job", zap.String("job_id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"message": "job cancelled"})
}

func (h *Handler) Retry(c *gin.Context) {
	id := c.Param("id")

	job, err := h.service.Retry(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to retry job", zap.String("job_id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"job": job})
}

func (h *Handler) Events(c *gin.Context) {
	id := c.Param("id")

	events, err := h.service.ListEvents(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to list job events", zap.String("job_id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"events": events, "count": len(events)})
}

// GetFull godoc
// @Summary Get full job details
// @Description Get job with events and full status
// @Tags jobs
// @Accept json
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]string
// @Router /jobs/{id}/full [get]
func (h *Handler) GetFull(c *gin.Context) {
	id := c.Param("id")

	job, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		apiutil.NotFound(c, "job not found")
		return
	}

	events, err := h.service.ListEvents(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to list job events", zap.String("job_id", id), zap.Error(err))
		// Don't fail - just return empty events
		events = make([]models.JobEvent, 0)
	}

	retryable := job.CanRetry()

	apiutil.OK(c, gin.H{
		"id":           job.ID,
		"type":         job.Type,
		"status":       job.Status,
		"progress":     job.Progress,
		"current_step": job.Status, // TODO: add current_step to job model
		"events":       events,
		"result":       job.Result,
		"retryable":    retryable,
		"job":          job,
	})
}
