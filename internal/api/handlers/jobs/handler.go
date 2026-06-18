package jobs

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler exposes HTTP endpoints for job lifecycle management.
type Handler struct {
	service *jobs.Service
	log     *zap.Logger
}

// NewHandler creates a new jobs HTTP handler.
func NewHandler(service *jobs.Service, log *zap.Logger) *Handler {
	return &Handler{service: service, log: log}
}

// RegisterRoutes mounts the job endpoints under the given router group.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("", h.Enqueue)
	r.GET("", h.List)
	r.GET("/stats", h.Stats)
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

	j, err := h.service.Enqueue(c.Request.Context(), &req)
	if err != nil {
		h.log.Error("failed to enqueue job", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.Accepted(c, gin.H{
		"job_id": j.ID,
		"job": gin.H{
			"id":       j.ID,
			"type":     j.Type,
			"status":   j.Status,
			"project":  j.Project,
			"progress": j.Progress,
		},
	})
}

func (h *Handler) Get(c *gin.Context) {
	id := c.Param("id")

	j, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		apiutil.NotFound(c, "job not found")
		return
	}

	apiutil.OK(c, gin.H{"job": j})
}

func (h *Handler) List(c *gin.Context) {
	var filter job.Filter

	if status := c.Query("status"); status != "" {
		s := job.Status(status)
		filter.Status = &s
	}
	if jobType := c.Query("type"); jobType != "" {
		filter.Type = &jobType
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

	jobsList, err := h.service.List(c.Request.Context(), filter)
	if err != nil {
		h.log.Error("failed to list jobs", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"jobs": jobsList, "count": len(jobsList)})
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

	j, err := h.service.Retry(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to retry job", zap.String("job_id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"job": j})
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

// Stats returns aggregated job statistics for monitoring.
func (h *Handler) Stats(c *gin.Context) {
	stats, err := h.service.GetStats(c.Request.Context())
	if err != nil {
		h.log.Error("failed to get job stats", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"stats": stats})
}

func (h *Handler) GetFull(c *gin.Context) {
	id := c.Param("id")

	j, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		apiutil.NotFound(c, "job not found")
		return
	}
	if j == nil {
		apiutil.NotFound(c, "job not found")
		return
	}

	events, err := h.service.ListEvents(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to list job events", zap.String("job_id", id), zap.Error(err))
		events = make([]job.Event, 0)
	}

	retryable := j.CanRetry()

	apiutil.OK(c, gin.H{
		"id":           j.ID,
		"type":         j.Type,
		"status":       j.Status,
		"progress":     j.Progress,
		"current_step": j.Status,
		"events":       events,
		"result":       j.Result,
		"retryable":    retryable,
		"job":          j,
	})
}
