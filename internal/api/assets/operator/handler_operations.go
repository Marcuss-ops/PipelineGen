// Package operator — handler_operations.go (RESOURCE: OPERATIONS, July 2026).
//
// Split rationale (resource/handler), see handler.go header.
//
// This file owns the OPERATIONS resource (read-only operational
// diagnostics for the admin dashboard). Routes:
//
//   - GET /operations/errors → handleOperationsErrors
//
// registers via the private registerOperationsRoutes method, called from
// handler.go::RegisterRoutes.
package operator

import (
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// registerOperationsRoutes mounts operations transports on the shared
// /api/assets/operator/* prefix. The paths "/operations/errors" is
// RELATIVE to the parent router group.
func (h *Handler) registerOperationsRoutes(rg *gin.RouterGroup) {
	rg.GET("/operations/errors", h.handleOperationsErrors)
}

// handleOperationsErrors returns recent operational errors:
//   - latest failed jobs
//   - outbox events carrying a last_error (pending/processing/dead_letter)
//
// It is read-only and does not expose worker tokens or internal routes.
func (h *Handler) handleOperationsErrors(c *gin.Context) {
	ctx := c.Request.Context()
	resp := gin.H{
		"ok":            true,
		"failed_jobs":   []gin.H{},
		"outbox_errors": []any{},
	}

	// Latest failed jobs
	if h.jobService != nil {
		failedStatus := job.StatusFailed
		failedJobs, err := h.jobService.List(ctx, job.Filter{Status: &failedStatus, Limit: 10})
		if err != nil {
			h.log.Warn("failed to list failed jobs", zap.Error(err))
		} else {
			resp["failed_jobs"] = h.jobsToJSON(failedJobs)
		}
	}

	// Outbox events with errors (pending/processing/dead_letter/completed)
	if h.outboxPort != nil {
		var outboxErrors []any
		for _, status := range []string{"pending", "processing", "dead_letter", "completed"} {
			events, err := h.outboxPort.ListByStatus(ctx, status)
			if err != nil {
				h.log.Warn("failed to list outbox events for errors", zap.String("status", status), zap.Error(err))
				continue
			}
			for i, e := range events {
				if i >= 50 {
					break
				}
				if e.LastError != "" {
					outboxErrors = append(outboxErrors, e)
				}
			}
		}
		resp["outbox_errors"] = outboxErrors
	}

	apiutil.OK(c, resp)
}
