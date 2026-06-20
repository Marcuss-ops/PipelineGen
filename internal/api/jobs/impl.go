package jobs

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// JobsHandler exposes HTTP endpoints for job lifecycle management.
type JobsHandler struct {
	service *appjobs.Service
	log     *zap.Logger
}

// NewJobsHandler creates a new jobs HTTP handler.
func NewJobsHandler(service *appjobs.Service, log *zap.Logger) *JobsHandler {
	return &JobsHandler{service: service, log: log}
}

// RegisterRoutes mounts the job endpoints under the given router group.
func (h *JobsHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("", h.Enqueue)
	r.GET("", h.List)
	r.GET("/stats", h.Stats)
	r.GET("/:id", h.Get)
	r.GET("/:id/full", h.GetFull)
	r.POST("/:id/cancel", h.Cancel)
	r.POST("/:id/retry", h.Retry)
	r.GET("/:id/events", h.Events)
}

func (h *JobsHandler) Enqueue(c *gin.Context) {
	req, ok := api.BindJSON[appjobs.EnqueueRequest](c)
	if !ok {
		return
	}

	j, err := h.service.Enqueue(c.Request.Context(), &req)
	if err != nil {
		h.log.Error("failed to enqueue job", zap.Error(err))
		api.InternalError(c, err)
		return
	}

	api.Accepted(c, gin.H{
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

func (h *JobsHandler) Get(c *gin.Context) {
	id := c.Param("id")

	j, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		api.NotFound(c, "job not found")
		return
	}

	api.OK(c, gin.H{"job": j})
}

func (h *JobsHandler) List(c *gin.Context) {
	var filter domainjob.Filter

	if status := c.Query("status"); status != "" {
		s := domainjob.Status(status)
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
		api.InternalError(c, err)
		return
	}

	api.OK(c, gin.H{"jobs": jobsList, "count": len(jobsList)})
}

func (h *JobsHandler) Cancel(c *gin.Context) {
	id := c.Param("id")

	if err := h.service.Cancel(c.Request.Context(), id); err != nil {
		h.log.Error("failed to cancel job", zap.String("job_id", id), zap.Error(err))
		api.InternalError(c, err)
		return
	}

	api.OK(c, gin.H{"message": "job cancelled"})
}

func (h *JobsHandler) Retry(c *gin.Context) {
	id := c.Param("id")

	j, err := h.service.Retry(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to retry job", zap.String("job_id", id), zap.Error(err))
		api.InternalError(c, err)
		return
	}

	api.OK(c, gin.H{"job": j})
}

func (h *JobsHandler) Events(c *gin.Context) {
	id := c.Param("id")

	events, err := h.service.ListEvents(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to list job events", zap.String("job_id", id), zap.Error(err))
		api.InternalError(c, err)
		return
	}

	api.OK(c, gin.H{"events": events, "count": len(events)})
}

// Stats returns aggregated job statistics for monitoring.
func (h *JobsHandler) Stats(c *gin.Context) {
	stats, err := h.service.GetStats(c.Request.Context())
	if err != nil {
		h.log.Error("failed to get job stats", zap.Error(err))
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"stats": stats})
}

func (h *JobsHandler) GetFull(c *gin.Context) {
	id := c.Param("id")

	j, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		api.NotFound(c, "job not found")
		return
	}
	if j == nil {
		api.NotFound(c, "job not found")
		return
	}

	// On error, fall back to an empty slice so the response shape stays stable;
	// clients polling /full already expect events to be an array (never null).
	// The canonical Event type lives in domain/job.
	events := []domainjob.Event{}
	if eventsList, err := h.service.ListEvents(c.Request.Context(), id); err != nil {
		h.log.Error("failed to list job events", zap.String("job_id", id), zap.Error(err))
	} else {
		events = eventsList
	}

	retryable := j.CanRetry()

	api.OK(c, gin.H{
		"id":       j.ID,
		"type":     j.Type,
		"status":   j.Status,
		"progress": j.Progress,
		// current_step is preserved as j.Status for backward compatibility with
		// clients that already poll /full. A real "current step" (last event
		// message or workflow node) will be wired in a follow-up; do not remove
		// the field without bumping the API contract.
		"current_step": j.Status,
		"events":       events,
		"result":       j.Result,
		"retryable":    retryable,
		"job":          j,
	})
}
