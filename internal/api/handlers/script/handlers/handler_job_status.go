package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/pkg/apiutil"
)

// GetJobFullStatus returns the full job state including events.
func (h *ScriptFlowHandler) GetJobFullStatus(c *gin.Context) {
	if h.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}

	jobID := strings.TrimSpace(c.Param("job_id"))
	if jobID == "" {
		apiutil.BadRequest(c, "job_id is required")
		return
	}

	if !h.requireJobAuth(c) {
		return
	}

	job, err := h.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		apiutil.NotFound(c, fmt.Sprintf("job not found: %v", err))
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
func (h *ScriptFlowHandler) GetJobStatus(c *gin.Context) {
	if h.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}

	jobID := strings.TrimSpace(c.Param("job_id"))
	if jobID == "" {
		apiutil.BadRequest(c, "job_id is required")
		return
	}

	if !h.requireJobAuth(c) {
		return
	}

	job, err := h.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		apiutil.NotFound(c, fmt.Sprintf("job not found: %v", err))
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

// requireJobAuth enforces the X-Velox-Admin-Token check before returning
// job state. Reads token from X-Velox-Admin-Token header or Authorization:
// Bearer <token>. Returns true when auth is satisfied (or disabled).
func (h *ScriptFlowHandler) requireJobAuth(c *gin.Context) bool {
	if h.cfg == nil || !h.cfg.Security.EnableAuth {
		return true
	}
	token := strings.TrimSpace(c.GetHeader("X-Velox-Admin-Token"))
	if token == "" {
		authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		}
	}
	expected := strings.TrimSpace(h.cfg.Security.AdminToken)
	if expected == "" || token != expected {
		h.log.Warn("rejected job-status request without admin token",
			zap.String("path", c.Request.URL.Path),
			zap.String("client_ip", c.ClientIP()))
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "admin token required to read job status",
		})
		return false
	}
	return true
}
