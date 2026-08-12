package jobs

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
	service job.Service
	stats   appjobs.JobStatsReader
	history appjobs.HistoryReader
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
func NewJobsHandler(service job.Service, stats appjobs.JobStatsReader, log *zap.Logger) *JobsHandler {
	return &JobsHandler{service: service, stats: stats, log: log}
}

func (h *JobsHandler) SetHistoryReader(reader appjobs.HistoryReader) { h.history = reader }

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

func (h *JobsHandler) History(c *gin.Context) {
	if h.history == nil {
		apiutil.Error(c, 503, "operation history is not configured")
		return
	}
	f := appjobs.HistoryFilter{Status: c.Query("status"), Type: c.Query("type")}
	if raw := c.Query("from"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			f.From = &parsed
		} else {
			apiutil.Error(c, 400, "invalid from timestamp")
			return
		}
	}
	if raw := c.Query("to"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			f.To = &parsed
		} else {
			apiutil.Error(c, 400, "invalid to timestamp")
			return
		}
	}
	f.Limit, _ = strconv.Atoi(c.DefaultQuery("limit", "200"))
	f.Offset, _ = strconv.Atoi(c.DefaultQuery("offset", "0"))
	items, err := h.history.ListHistory(c.Request.Context(), f)
	if err != nil {
		h.log.Error("failed to list operation history", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"history": items, "count": len(items), "limit": f.Limit, "offset": f.Offset})
}

func (h *JobsHandler) Enqueue(c *gin.Context) {
	dto, ok := apiutil.BindJSON[appjobs.EnqueueRequest](c)
	if !ok {
		return
	}

	// Map HTTP DTO to domain request
	req := job.EnqueueRequest{
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
	if j == nil {
		apiutil.NotFound(c, "job not found")
		return
	}

	events, _ := h.service.ListEvents(c.Request.Context(), id)
	apiutil.OK(c, h.buildJobResponse(j, events))
}

func (h *JobsHandler) List(c *gin.Context) {
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
	if correlationID := c.Query("correlation_id"); correlationID != "" {
		filter.CorrelationID = &correlationID
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
		Retry(context.Context, string) (*job.Job, error)
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
	events := []job.Event{}
	if eventsList, err := h.service.ListEvents(c.Request.Context(), id); err != nil {
		h.log.Error("failed to list job events", zap.String("job_id", id), zap.Error(err))
	} else {
		events = eventsList
	}

	// PR-ERROR-SURFACING (2026-07-04): godlike/06 SSOT between `/api/jobs` (LIST)
	// and `/api/jobs/{id}/full` (GET) — both endpoints MUST surface the canonical
	// job.Error field at TOP-level so a polled `/full` does not silently drop
	// typed-error strings (e.g. `scriptpkg.ErrScriptGenerationFailed` wraps
	// accumulated through the worker → jobs.error column → j.Error struct
	// field). The LIST endpoint already exposes each slice element's
	// `error` JSON tag (canonical `json:"error,omitempty"` in
	// internal/kernel/job/job.go::Job.Error); the /full response, which
	// historically enumerated only id/type/status/progress/current_step/events/
	// result/retryable/job in gin.H{}, DROPPED the `error` field at the
	// top-level (operators reading `/full` saw `error=None` even when the
	// DB column had a 123-char error string). The fix adds `error` to the
	// gin.H literal so parity with LIST holds end-to-end.
	//
	// Behaviour preservation: the canonical `job: j` embedded object
	// continues to expose `job.error` (unchanged), so callers using the
	// nested path keep working. The new top-level `error` is the canonical
	// surface for /full parity with /api/jobs LIST.
	apiutil.OK(c, h.buildJobResponse(j, events))
}

// buildJobResponse assembles the canonical enriched job status shape
// shared by GET /api/jobs/{id} and GET /api/jobs/{id}/full.
// It derives current_stage from the most recent timeline event and
// surfaces any events whose type is "warning".
func (h *JobsHandler) buildJobResponse(j *job.Job, events []job.Event) gin.H {
	currentStage := string(j.Status)
	warnings := make([]gin.H, 0)
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != "" && events[i].Type != "warning" {
			currentStage = events[i].Type
			break
		}
	}
	for _, e := range events {
		if e.Type == "warning" {
			warnings = append(warnings, gin.H{
				"event_id": e.ID,
				"message":  e.Message,
				"data":     e.Data,
			})
		}
	}

	return gin.H{
		"id":             j.ID,
		"type":           j.Type,
		"status":         j.Status,
		"correlation_id": j.CorrelationID,
		"current_stage":  currentStage,
		"current_step":   j.Status,
		"progress":       j.Progress,
		"warnings":       warnings,
		"result":         j.Result,
		"error":          j.Error,
		"created_at":     j.CreatedAt,
		"started_at":     j.StartedAt,
		"updated_at":     j.UpdatedAt,
		"timeline":       events,
		"events":         events,
		"retryable":      j.CanRetry(),
		"job":            j,
	}
}
