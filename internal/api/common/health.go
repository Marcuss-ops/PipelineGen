// Package common provides shared HTTP handlers. The HealthHandler delegates
// to the application-layer health.Service so the transport stays thin:
// no database/sql, no Google Drive SDK, no Qdrant HTTP calls, no
// file-system reads inside the handler.
//
// Query parameters:
//
//	?deep=true           run all component checks (default: fast ping only)
//	?check=db,drive,...  run only the named checks (implies deep)
//
// Response shape:
//
//	{
//	  "ok": true,
//	  "status": "healthy",
//	  "checks": {
//	    "db":     {"ok": true, "duration_ms": 2},
//	    "drive":  {"ok": true, "duration_ms": 145},
//	    "qdrant": {"ok": true, "duration_ms": 12, "points_count": 1500},
//	    "jobs":   {"ok": true, "duration_ms": 1}
//	  }
//	}
//
// When no deep parameter is supplied the response is the legacy short form:
//
//	{"ok": true, "status": "healthy"}
package common

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	health "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
)

// HealthHandler handles health check requests. All infrastructure work
// is delegated to the application-layer health.Service.
type HealthHandler struct {
	svc *health.Service
}

// NewHealthHandler creates a new health handler.
func NewHealthHandler(svc *health.Service) *HealthHandler {
	return &HealthHandler{svc: svc}
}

// Ready godoc
// @Summary Readiness check
// @Description Verifies critical dependencies: database accessibility, migrations applied.
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /ready [get]
func (h *HealthHandler) Ready(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"ok":     false,
			"reason": "health service not initialized",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp := h.svc.Check(ctx, []string{"db"})
	if resp.OK {
		c.JSON(http.StatusOK, gin.H{
			"status": "ready",
			"ok":     true,
			"checks": resp.Checks,
		})
	} else {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"ok":     false,
			"checks": resp.Checks,
		})
	}
}

// ── Health (GET /health) ──────────────────────────────────────────────

// Health godoc
// @Summary Unified health check
// @Description Single modular health endpoint aggregating DB+Drive+Qdrant+JobBroker.
// @Description Use ?deep=true for full component checks; ?check=db,drive,... for granular.
// @Tags health
// @Accept json
// @Produce json
// @Param deep query bool false "Run all component checks"
// @Param check query string false "Comma-separated list: db,drive,qdrant,jobs"
// @Success 200 {object} map[string]any
// @Router /health [get]
func (h *HealthHandler) Health(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unhealthy",
			"ok":     false,
			"reason": "health service not initialized",
		})
		return
	}

	// Determine check depth.
	deep := c.Query("deep") == "true"
	checkParam := strings.TrimSpace(c.Query("check"))
	checksRequested := checkParam != ""

	// Fast path: lightweight ping only.
	if !deep && !checksRequested {
		c.JSON(http.StatusOK, gin.H{
			"status": "healthy",
			"ok":     true,
		})
		return
	}

	// Build the allowlist of checks to run.
	var names []string
	if checksRequested {
		for _, name := range strings.Split(checkParam, ",") {
			names = append(names, strings.TrimSpace(name))
		}
	} else {
		// deep=true without check= → run all.
		names = []string{"db", "drive", "qdrant", "jobs"}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	resp := h.svc.Check(ctx, names)

	// Return 503 when any component check fails (PR1 fix: deep health
	// previously returned 200 even when unhealthy). The fast-ping path
	// (no deep/check params) still returns 200 for load-balancer convenience.
	statusCode := http.StatusOK
	if !resp.OK {
		statusCode = http.StatusServiceUnavailable
	}

	c.JSON(statusCode, gin.H{
		"ok":     resp.OK,
		"status": resp.Status,
		"checks": resp.Checks,
	})
}
