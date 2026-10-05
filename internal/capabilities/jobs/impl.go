package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	mwm2m "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// JobsHandler exposes HTTP endpoints for job lifecycle management.
//
// PR-0 (June 2026): split into (domain Service, JobStatsReader). Service is
// the canonical domain interface (job.Service); the stats reader is a
// narrow port (JobStatsReader) exposing the SQLite-specific GetStats
// helper without leaking it onto the orchestrator surface.
// *Service satisfies both interfaces — composition-root wiring
// passes the same concrete pointer to both fields. The Stats
// endpoint consumes only the reader; Enqueue/Cancel/Retry/etc.
// consume only the orchestrator. The split is intentional so a future
// Postgres migration can wire a different stats reader (e.g. one that
// aggregates across shards) without touching the orchestrator's
// mutation surface.
type JobsHandler struct {
	service  job.Service
	stats    JobStatsReader
	history  HistoryReader
	replay   *replayConfig
	log      *zap.Logger
	schedule job.ScheduleStore
	stages   job.JobStageStatusStore
}

// NewJobsHandler creates a new jobs HTTP handler.
//
// PR-0 (June 2026): signature expanded to (job.Service,
// JobStatsReader). Canonical composition root passes `Service`
// for both fields (it satisfies both via compile-time assertion in
// internal/capabilities/jobs/queue/stats.go). A reader-only binding (e.g.
// a Postgres-backed aggregator without the mutation surface) passes
// an implementation that satisfies only JobStatsReader.
func NewJobsHandler(service job.Service, stats JobStatsReader, log *zap.Logger) *JobsHandler {
	return &JobsHandler{service: service, stats: stats, log: log}
}

func (h *JobsHandler) SetHistoryReader(reader HistoryReader) { h.history = reader }

// SetScheduling attaches the optional deferred-scheduling and per-stage
// status ports. Both are nil-tolerant: when a port is absent the
// corresponding route answers 503 instead of panicking, so a deployment
// without a scheduling-capable broker degrades explicitly.
func (h *JobsHandler) SetScheduling(schedule job.ScheduleStore, stages job.JobStageStatusStore) {
	h.schedule = schedule
	h.stages = stages
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
	r.POST("/:id/replay", h.Replay)
	// Deferred scheduling + per-stage status (migration 005).
	r.GET("/scheduled", h.ListScheduled)
	r.POST("/schedule", h.ScheduleBatch)
	r.GET("/:id/stages", h.ListStages)
	r.PATCH("/:id/stages/:stage", h.UpdateStage)
}

func (h *JobsHandler) History(c *gin.Context) {
	if h.history == nil {
		apiutil.Error(c, 503, "operation history is not configured")
		return
	}
	f := HistoryFilter{Status: c.Query("status"), Type: c.Query("type")}
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
	dto, ok := apiutil.BindJSON[EnqueueRequest](c)
	if !ok {
		return
	}

	// M2M must submit only an explicitly external-safe capability. A live
	// handler alone is insufficient: internal maintenance and child jobs can
	// also have consumers but are not an agent contract.
	if _, isM2M := c.Get("m2m_client"); isM2M {
		catalog, ok := h.service.(interface{ AutomationCatalog() *AutomationCatalog })
		if !ok || catalog.AutomationCatalog() == nil {
			apiutil.Error(c, http.StatusInternalServerError, "automation catalog is not configured")
			return
		}
		if !catalog.AutomationCatalog().Allows(dto.Type) {
			apiutil.Error(c, http.StatusUnprocessableEntity, "job type is not external-safe: "+dto.Type)
			return
		}
	}

	// PG-M2M (Aug 2026): resolve the M2M client_id from the gin context.
	// JobClientAuthMiddleware stores the resolved *M2MClient under
	// jobClientContextKey when the request came through the M2M surface
	// (/api/v1/jobs). On the admin surface (/api/jobs) no M2MClient is
	// stored, so client_id stays empty — admin enqueues are NOT deduped
	// by (client_id, idempotency_key), preserving the existing admin
	// semantics. The client_id is the non-secret projection of the
	// Bearer VELOX_M2M_SECRET; the plaintext token is never stored.
	var clientID string
	if raw, exists := c.Get("m2m_client"); exists && raw != nil {
		if m2mClient, ok := raw.(*mwm2m.M2MClient); ok && m2mClient != nil {
			clientID = m2mClient.ClientID
		}
	}

	j, err := h.service.Enqueue(c.Request.Context(), h.domainEnqueueRequest(dto, clientID))
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

// M2MTypes exposes the canonical external-safe automation catalog. Remote
// computers use it to discover stable tools; internal and orphaned jobs are
// never advertised.
func (h *JobsHandler) M2MTypes(c *gin.Context) {
	catalog, ok := h.service.(interface{ AutomationCatalog() *AutomationCatalog })
	if !ok || catalog.AutomationCatalog() == nil {
		apiutil.Error(c, http.StatusInternalServerError, "automation catalog is not configured")
		return
	}
	apiutil.OK(c, gin.H{"types": catalog.AutomationCatalog().List()})
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

// M2MGet limits status polling to jobs created by the authenticated M2M client.
func (h *JobsHandler) M2MGet(c *gin.Context) {
	raw, exists := c.Get("m2m_client")
	client, ok := raw.(*mwm2m.M2MClient)
	if !exists || !ok || client == nil || client.ClientID == "" {
		apiutil.Error(c, http.StatusUnauthorized, "M2M client identity is required")
		return
	}
	id := c.Param("id")
	j, err := h.service.Get(c.Request.Context(), id)
	if err != nil || j == nil || j.ClientID != client.ClientID {
		apiutil.NotFound(c, "job not found")
		return
	}
	events, _ := h.service.ListEvents(c.Request.Context(), id)
	apiutil.OK(c, h.buildJobResponse(j, events))
}

// M2MCancel permits a submitter to stop only a job created by the same M2M client.
func (h *JobsHandler) M2MCancel(c *gin.Context) {
	raw, exists := c.Get("m2m_client")
	client, ok := raw.(*mwm2m.M2MClient)
	if !exists || !ok || client == nil || client.ClientID == "" {
		apiutil.Error(c, http.StatusUnauthorized, "M2M client identity is required")
		return
	}
	id := c.Param("id")
	j, err := h.service.Get(c.Request.Context(), id)
	if err != nil || j == nil || j.ClientID != client.ClientID {
		apiutil.NotFound(c, "job not found")
		return
	}
	if err := h.service.Cancel(c.Request.Context(), id); err != nil {
		h.log.Error("failed to cancel M2M-owned job", zap.String("job_id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"job_id": id, "status": "CANCELLED"})
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

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func activitySequenceValue(value any) uint64 {
	switch sequence := value.(type) {
	case float64:
		if sequence > 0 {
			return uint64(sequence)
		}
	case uint64:
		return sequence
	case uint:
		return uint64(sequence)
	case int:
		if sequence > 0 {
			return uint64(sequence)
		}
	case int64:
		if sequence > 0 {
			return uint64(sequence)
		}
	case json.Number:
		parsed, _ := strconv.ParseUint(string(sequence), 10, 64)
		return parsed
	}
	return 0
}

func activityTraceProjection(data map[string]any) map[string]any {
	trace := make(map[string]any)
	for _, key := range []string{"run_id", "attempt_id", "parent_run_id", "correlation_id", "sequence"} {
		if value, exists := data[key]; exists {
			trace[key] = value
		}
	}
	if nested, ok := data["trace"].(map[string]any); ok {
		for key, value := range nested {
			if _, exists := trace[key]; !exists {
				trace[key] = value
			}
		}
	}
	if len(trace) == 0 {
		return nil
	}
	return trace
}

// buildJobResponse assembles the canonical enriched job status shape
// shared by GET /api/jobs/{id} and GET /api/jobs/{id}/full.
// It derives current_stage from the most recent timeline event and
// surfaces any events whose type is "warning".
func (h *JobsHandler) buildJobResponse(j *job.Job, events []job.Event) gin.H {
	currentStage := string(j.Status)
	currentKind := j.Type
	currentSubKind := ""
	currentMicroKind := ""
	currentStatus := string(j.Status)
	currentDetail := ""
	var currentPayload map[string]any
	var currentTrace map[string]any
	currentSequence := uint64(0)
	currentRunID, currentAttemptID, currentParentRunID, currentCorrelationID := "", "", "", ""
	warnings := make([]gin.H, 0)
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "" || events[i].Type == "warning" {
			continue
		}
		currentStage = events[i].Type
		if events[i].Data == nil {
			currentSubKind = events[i].Type
			currentMicroKind = events[i].Type
			currentDetail = events[i].Message
		} else {
			if value, ok := events[i].Data["kind"].(string); ok && value != "" {
				currentKind = value
			}
			if value, ok := events[i].Data["sub_kind"].(string); ok {
				currentSubKind = value
			}
			if value, ok := events[i].Data["micro_kind"].(string); ok && value != "" {
				currentMicroKind = value
			} else {
				currentMicroKind = currentSubKind
			}
			if value, ok := events[i].Data["status"].(string); ok && value != "" {
				currentStatus = value
			}
			if value, ok := events[i].Data["detail"].(string); ok {
				currentDetail = value
			}
			if value, ok := events[i].Data["payload"].(map[string]any); ok {
				currentPayload = value
			} else {
				currentPayload = events[i].Data
			}
			currentTrace = activityTraceProjection(events[i].Data)
			currentSequence = activitySequenceValue(currentTrace["sequence"])
			currentRunID = stringValue(currentTrace["run_id"])
			currentAttemptID = stringValue(currentTrace["attempt_id"])
			currentParentRunID = stringValue(currentTrace["parent_run_id"])
			currentCorrelationID = stringValue(currentTrace["correlation_id"])
		}
		if currentSubKind == "" {
			currentSubKind = events[i].Type
		}
		if currentMicroKind == "" {
			currentMicroKind = currentSubKind
		}
		if currentDetail == "" {
			currentDetail = events[i].Message
		}
		break
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
		"id":                     j.ID,
		"type":                   j.Type,
		"status":                 j.Status,
		"correlation_id":         j.CorrelationID,
		"current_stage":          currentStage,
		"current_step":           j.Status,
		"current_kind":           currentKind,
		"current_sub_kind":       currentSubKind,
		"current_micro_kind":     currentMicroKind,
		"current_status":         currentStatus,
		"current_detail":         currentDetail,
		"current_payload":        currentPayload,
		"current_trace":          currentTrace,
		"current_sequence":       currentSequence,
		"current_run_id":         currentRunID,
		"current_attempt_id":     currentAttemptID,
		"current_parent_run_id":  currentParentRunID,
		"current_correlation_id": currentCorrelationID,
		"progress":               j.Progress,
		"warnings":               warnings,
		"result":                 j.Result,
		"error":                  j.Error,
		"created_at":             j.CreatedAt,
		"started_at":             j.StartedAt,
		"lease_expiry":           j.LeaseExpiry,
		"worker_id":              j.WorkerID,
		"updated_at":             j.UpdatedAt,
		"timeline":               events,
		"events":                 events,
		"retryable":              j.CanRetry(),
		"job":                    j,
	}
}
