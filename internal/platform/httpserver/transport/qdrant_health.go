// Package transport — Qdrant-specific health endpoints (THIN TRANSPORT).
//
// HIGH #7 (Qdrant Verdetto, July 2026): exposes /qdrant/live and
// /qdrant/ready as dedicated Qdrant health probes, separate from the
// generic /health and /ready endpoints that aggregate DB+Drive+Qdrant.
//
// /qdrant/live: fast liveness — QdrantEndpointPort.Live (5s timeout at
// the adapter). Returns 200 if Live returns nil, 503 otherwise.
//
// /qdrant/ready: deep readiness with 4 named sub-checks
// (alias / collection / schema / canary), produced by
// QdrantEndpointPort.Ready. The handler is pure JSON transport —
// adapter-owned 4-check semantics + canary cache (10s full-sweep
// timeout at the adapter).
//
// Sprint 3.4 step1: the handler imports ONLY
// internal/application/system/health (the QdrantEndpointPort port).
// Composition-root adapter lives in
// internal/app/qdrant_transport_health_adapter.go and is the ONLY
// place allowed to wire disasterrecovery.HealthProbe +
// collections.CollectionManager + search.Searcher +
// search.TextEmbedder into the port.
package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"

	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
)

// QdrantHealthHandler exposes /qdrant/live and /qdrant/ready.
// All Qdrant operations are delegated to
// systemhealth.QdrantEndpointPort; nil-safe for transport-layer
// composition when Qdrant is disabled.
type QdrantHealthHandler struct {
	port systemhealth.QdrantEndpointPort
}

// NewQdrantHealthHandler constructs a handler. port may be nil
// (matches the previous nil-safe contract: nil ports render 503
// with a clear "not configured" message).
func NewQdrantHealthHandler(port systemhealth.QdrantEndpointPort) *QdrantHealthHandler {
	return &QdrantHealthHandler{port: port}
}

// Live is GET /qdrant/live — fast liveness via port.Live.
// Renders 200 if the port returns nil; 503 with status="not live"
// + the port's error otherwise.
func (h *QdrantHealthHandler) Live(c *gin.Context) {
	if h.port == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":     false,
			"status": "not live",
			"error":  "qdrant health probe not configured",
		})
		return
	}
	if err := h.port.Live(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":     false,
			"status": "not live",
			"error":  err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "status": "live"})
}

// Ready is GET /qdrant/ready — deep readiness report produced by
// port.Ready. The handler maps report.OK → HTTP 200 / 503.
// Special-case: when report.Error is set (adapter not-configured
// short-circuit), render the legacy flat wire shape verbatim
// (preserves backwards-compatible JSON contract).
func (h *QdrantHealthHandler) Ready(c *gin.Context) {
	if h.port == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":     false,
			"status": "not ready",
			"error":  "qdrant endpoint port not configured",
		})
		return
	}
	report := h.port.Ready(c.Request.Context())
	status := http.StatusOK
	if !report.OK {
		status = http.StatusServiceUnavailable
	}
	if report.Error != "" {
		// Legacy flat wire shape: {ok, status, error}. Adapter-side
		// not-configured branch sets ONLY Error; Checks is nil there.
		c.JSON(status, gin.H{
			"ok":     report.OK,
			"status": report.Status,
			"error":  report.Error,
		})
		return
	}
	c.JSON(status, report)
}
