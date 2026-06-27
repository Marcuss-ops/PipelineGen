// Package common — HTTP handlers for system-wide endpoints (health, ready).
//
// fix(health) close-out (June 2026, problem #2 final cleanup): the
// readiness policy moved out of this handler into
// application/system/health.ReadyChecker. The handler is now thin
// transport (Pattern 8 from AGENTS.md): it asks the checker, maps
// ready.OK -> HTTP 200 / 503, and serializes the response.
//
// Health contract (codex/health-ready-contract, June 2026):
//   - GET /health → fast liveness, no automatic dependency checks, HTTP 200
//   - GET /health?deep=true → full set (db, drive, qdrant, jobs)
//   - GET /health?check=db&check=jobs → repeated query params
//   - GET /health?check=db,jobs → comma-separated (compatibility)
//   - Unknown check → HTTP 400 (typed ErrUnknownCheck)
//   - Empty names after normalisation → fast liveness
package transport

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/gin-gonic/gin"
)

// HealthHandler exposes /health and /ready endpoints.
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

// fastHealthNames is the default deep-check set used when ?deep=true.
var fastHealthNames = []string{"db", "drive", "qdrant", "jobs"}

// Health serves /health with the canonical contract:
//   - No params → fast liveness (all names empty → Service returns
//     {ok: true, status: "healthy"} without touching any dependency).
//   - ?deep=true → full set (db, drive, qdrant, jobs).
//   - ?check=X[&check=Y] → repeated query params.
//   - ?check=X,Y → comma-separated (compatibility).
//   - Unknown check name → HTTP 400 (typed ErrUnknownCheck).
func (h *HealthHandler) Health(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "status": "unhealthy", "error": "health service not initialized"})
		return
	}

	var names []string

	deep := strings.ToLower(strings.TrimSpace(c.Query("deep")))
	if deep == "true" || deep == "1" {
		names = fastHealthNames
	} else if checkVals, ok := c.Request.URL.Query()["check"]; ok && len(checkVals) > 0 {
		names = systemhealth.NormalizeCheckNames(checkVals)
		if len(names) == 0 {
			// All check values were empty/whitespace after normalisation:
			// fall through to fast liveness.
			c.JSON(http.StatusOK, systemhealth.HealthResponse{OK: true, Status: "healthy"})
			return
		}
		if err := systemhealth.ValidateCheckNames(names); err != nil {
			var unknownErr *systemhealth.ErrUnknownCheck
			if errors.As(err, &unknownErr) {
				c.JSON(http.StatusBadRequest, gin.H{
					"ok":     false,
					"status": "bad request",
					"error":  unknownErr.Error(),
				})
				return
			}
		}
	}
	// else: no params → fast liveness (names stays nil/empty)

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
