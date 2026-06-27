package jobs

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// JobsHandler exposes HTTP endpoints for job lifecycle management.
//
// PR-0 (June 2026): split into (domain Service, JobStatsReader). Service is
// the canonical domain interface (job.Service); the stats reader is a
// narrow port (JobStatsReader) exposing the SQLite-specific GetStats
// helper without leaking it onto the orchestrator surface.
// *appjobs.Service satisfies both interfaces — composition-root wiring
// passes the same concrete pointer to both fields. The Stats
// endpoint consumes only the reader; Enqueue/Cancel/Retry/etc.
// consume only the orchestrator. The split is intentional so a future
// Postgres migration can wire a different stats reader (e.g. one that
// aggregates across shards) without touching the orchestrator's
// mutation surface.
type JobsHandler struct {
	service domainjob.Service
	stats   appjobs.JobStatsReader
	log     *zap.Logger
}

// NewJobsHandler creates a new jobs HTTP handler.
//
// PR-0 (June 2026): signature expanded to (job.Service,
// JobStatsReader). Canonical composition root passes `jobs.Service`
// for both fields (it satisfies both via compile-time assertion in
// internal/application/jobs/stats.go). A reader-only binding (e.g.
// a Postgres-backed aggregator without the mutation surface) passes
// an implementation that satisfies only JobStatsReader.
func NewJobsHandler(service domainjob.Service, stats appjobs.JobStatsReader, log *zap.Logger) *JobsHandler {
	return &JobsHandler{service: service, stats: stats, log: log}
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
	dto, ok := apiutil.BindJSON[appjobs.EnqueueRequest](c)
	if !ok {
		return
	}

	// Map HTTP DTO to domain request
	req := domainjob.EnqueueRequest{
		Type:          dto.Type,
		Project:       dto.Project,
		VideoName:     dto.VideoName,
		Payload:       dto.Payload,
		Priority:      dto.Priority,
		MaxRetries:    dto.MaxRetries,
		ActiveKey:     dto.ActiveKey,
		CorrelationID: dto.CorrelationID,
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

func (h *JobsHandler) Get(c *gin.Context) {
	id := c.Param("id")

	j, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		apiutil.NotFound(c, "job not found")
		return
	}

	apiutil.OK(c, gin.H{"job": j})
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
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"jobs": jobsList, "count": len(jobsList)})
}

func (h *JobsHandler) Cancel(c *gin.Context) {
	id := c.Param("id")

	if err := h.service.Cancel(c.Request.Context(), id); err != nil {
		h.log.Error("failed to cancel job", zap.String("job_id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"message": "job cancelled"})
}

func (h *JobsHandler) Retry(c *gin.Context) {
	id := c.Param("id")

	type retryer interface {
		Retry(context.Context, string) (*domainjob.Job, error)
	}
	r, ok := h.service.(retryer)
	if !ok {
		apiutil.InternalError(c, fmt.Errorf("job retry not supported by wired service"))
		return
	}

	j, err := r.Retry(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to retry job", zap.String("job_id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"job": j})
}

func (h *JobsHandler) Events(c *gin.Context) {
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
//
// PR-0 (June 2026): reads from h.stats (the dedicated JobStatsReader
// port), NOT h.service (the orchestrator). The previous code called
// h.service.GetStats, which leaked the SQLite-specific helper via
// type-assertion and tied the Stats endpoint to the orchestrator
// concrete. With the port split, Stats is reporter-only and the
// handler compiles against the narrow interface signature.
func (h *JobsHandler) Stats(c *gin.Context) {
	stats, err := h.stats.GetStats(c.Request.Context())
	if err != nil {
		h.log.Error("failed to get job stats", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"stats": stats})
}

func (h *JobsHandler) GetFull(c *gin.Context) {
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

	apiutil.OK(c, gin.H{
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
