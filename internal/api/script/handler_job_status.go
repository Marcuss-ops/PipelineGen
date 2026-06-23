// Package script (api/script) — handler_job_status.go holds the
// /api/script/jobs/:job_id and /api/script/jobs/:job_id/full handlers.
//
// PR4.F3 (June 2026): the in-handler auth check (h.requireJobAuth) is
// removed. Authentication is now applied at the route layer via
// middleware.RequireAdminToken (see internal/api/middleware/admin_token.go).
// The handler is purely a transport-layer decoder for the typed job
// state — no auth-state conditional inside the request handler.
//
// Why the move: the previous inline check was a god-object anti-pattern
// (handler owned dereferencing of h.cfg AND security-decision logic),
// and the exact same check is already mounted as middleware in three
// other places (Auth() on the protected group, WorkerAuth() on the
// internal group, isURL-bypass hooks). The handler should not carry
// credential logic.
package script

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
)

// GetJobFullStatus returns the full job state including events.
//
// Auth: middleware.RequireAdminToken (mounted at route registration
// in handler_flow.go::RegisterRoutesRemaining). The handler itself
// does NOT re-check the credential — if a request reaches this method
// without a valid admin token, the middleware already wrote the 401
// response and aborted the chain.
func (h *ScriptFlowHandler) GetJobFullStatus(c *gin.Context) {
	if h.jobsSvc == nil {
		api.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}

	jobID := strings.TrimSpace(c.Param("job_id"))
	if jobID == "" {
		api.BadRequest(c, "job_id is required")
		return
	}

	job, err := h.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		api.NotFound(c, fmt.Sprintf("job not found: %v", err))
		return
	}

	events, err := h.jobsSvc.ListEvents(c.Request.Context(), jobID)
	if err != nil {
		h.log.Warn("failed to list job events", zap.String("job_id", jobID), zap.Error(err))
		events = nil
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":             true,
		"job_id":         job.ID,
		"type":           job.Type,
		"status":         job.Status,
		"priority":       job.Priority,
		"progress":       job.Progress,
		"error":          job.Error,
		"result":         job.Result,
		"retry_count":    job.RetryCount,
		"max_retries":    job.MaxRetries,
		"correlation_id": job.CorrelationID,
		"created_at":     job.CreatedAt,
		"started_at":     job.StartedAt,
		"completed_at":   job.CompletedAt,
		"updated_at":     job.UpdatedAt,
		"events":         events,
	})
}

// GetJobStatus returns the lightweight job state (status/progress/error/result).
//
// Auth: see GetJobFullStatus's doc comment — applied at the route layer.
func (h *ScriptFlowHandler) GetJobStatus(c *gin.Context) {
	if h.jobsSvc == nil {
		api.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}

	jobID := strings.TrimSpace(c.Param("job_id"))
	if jobID == "" {
		api.BadRequest(c, "job_id is required")
		return
	}

	job, err := h.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		api.NotFound(c, fmt.Sprintf("job not found: %v", err))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"job_id":   job.ID,
		"status":   job.Status,
		"progress": job.Progress,
		"error":    job.Error,
		"result":   job.Result,
	})
}
