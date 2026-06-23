// Package common — HTTP handlers for system-wide endpoints (health, ready).
//
// fix(health) close-out (June 2026, problem #2 final cleanup): the
// readiness policy moved out of this handler into
// application/system/health.ReadyChecker. The handler is now thin
// transport (Pattern 8 from AGENTS.md): it asks the checker, maps
// ready.OK -> HTTP 200 / 503, and serializes the response.
package common

import (
	"context"
	"net/http"
	"time"

	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/gin-gonic/gin"
)

// HealthHandler exposes /healthz (deep check) and /readyz (readiness).
type HealthHandler struct {
	svc   *systemhealth.Service
	ready *systemhealth.ReadyChecker
}

// NewHealthHandler constructs the handler. Both deps are required;
// the previous version accepted just *Service, which allowed a nil
// ready to slip through silently when the policy was inline.
func NewHealthHandler(svc *systemhealth.Service, ready *systemhealth.ReadyChecker) *HealthHandler {
	return &HealthHandler{svc: svc, ready: ready}
}

// Health returns the multi-check report. Names are caller-driven
// (typically via query string). Default = readiness set.
func (h *HealthHandler) Health(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "health service not initialized"})
		return
	}
	names := c.QueryArray("check")
	if len(names) == 0 {
		names = []string{"db", "drive", "qdrant", "jobs"}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	resp := h.svc.Check(ctx, names)
	status := http.StatusOK
	if !resp.OK {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, resp)
}

// Ready is the readiness probe. Policy is owned by ReadyChecker;
// this handler only maps CheckReady.OK -> 200 vs 503.
func (h *HealthHandler) Ready(c *gin.Context) {
	if h.ready == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "ok": false, "error": "ready checker not initialized"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	resp := h.ready.CheckReady(ctx)
	status := http.StatusOK
	if !resp.OK {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{
		"status": statusString(resp.OK),
		"ok":     resp.OK,
		"checks": resp.Checks,
	})
}

func statusString(ok bool) string {
	if ok {
		return "ready"
	}
	return "not ready"
}
