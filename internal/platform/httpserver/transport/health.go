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
package httpserver

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
	// wire is the optional WireRegistry. When set, the /ready
	// response includes a "wire" field listing which API capabilities
	// are mounted. nil → wire field is still emitted (with all values
	// NOT_MOUNTED per the All() nil-safe contract) so operators
	// can detect a 404'd capability without grepping server logs.
	// Set via SetWireRegistry at composition root after the gin
	// engine has all routes registered.
	wire *WireRegistry
}

// NewHealthHandler constructs the handler. Both deps are required;
// the previous version accepted just *Service, which allowed a nil
// ready to slip through silently when the policy was inline.
func NewHealthHandler(svc *systemhealth.Service, ready *systemhealth.ReadyChecker) *HealthHandler {
	return &HealthHandler{svc: svc, ready: ready}
}

// SetWireRegistry wires the WireRegistry into the handler. nil-safe
// (passing nil resets the registry to the all-NOT_MOUNTED default).
// The composition root calls this after the gin engine has all
// routes registered (i.e. after Setup() has built the engine).
func (h *HealthHandler) SetWireRegistry(r *WireRegistry) {
	h.wire = r
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
//
// The response always includes a "wire" field (capability → MOUNTED /
// NOT_MOUNTED) so operators can detect a 404'd capability without
// grepping server logs — the canonical use case surfaced by the
// stale-binary incident on 2026-07-07 where /api/stock-pipeline/run
// returned 400 (validation) but the new binary didn't have stock
// wired; a /ready "wire: { stock: NOT_MOUNTED }" would have caught
// it in 5 seconds.
func (h *HealthHandler) Ready(c *gin.Context) {
	if h.ready == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"ok":     false,
			"error":  "ready checker not initialized",
			"wire":   h.wireMap(),
		})
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
		"wire":   h.wireMap(),
	})
}

// wireMap returns the wire surface, nil-safe. Always returns a
// non-nil map (all NOT_MOUNTED when the registry is unset) so the
// /ready JSON shape is stable for operator tooling.
func (h *HealthHandler) wireMap() map[string]string {
	return h.wire.All()
}

func statusString(ok bool) string {
	if ok {
		return "ready"
	}
	return "not ready"
}
