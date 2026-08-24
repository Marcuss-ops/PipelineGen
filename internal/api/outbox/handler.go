// Package outbox provides thin HTTP transport for the outbox events system
// (QDRANT-002). The handler exposes operator-facing read-only endpoints at
// /internal/v1/outbox for monitoring outbox health and listing pending events.
//
// This is the canonical internal outbox endpoint — the final gap from
// QDRANT-002 ("l'endpoint interno canonico non è montato nel server").
//
// Wave 14 PR5 (June 2026): the handler now depends on
// outbox.MonitorPort (declared in
// internal/capabilities/jobs/outbox/ports.go) instead of the concrete
// *outboxevents.Repository. Per AGENTS.md Pattern 8 ("API package:
// thin transport only, no concrete infrastructure imports") the
// adapter wrapping the concrete repo lives in
// internal/app/outbox_monitor_adapter.go; the composition root
// wires it via outboxapi.NewHandler(port, log).
package outbox

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler is the thin HTTP transport for outbox events monitoring.
type Handler struct {
	port outbox.MonitorPort
	log  *zap.Logger
}

// NewHandler creates a new outbox events HTTP handler. The handler is
// read-only by design — CountByStatus + ListPending are the two
// methods on outbox.MonitorPort exercised here.
func NewHandler(port outbox.MonitorPort, log *zap.Logger) *Handler {
	return &Handler{port: port, log: log}
}

// RegisterRoutes mounts the outbox endpoints under the given router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/status", h.handleStatus)
	rg.GET("/events", h.handleEvents)
}

// handleStatus returns aggregated outbox event counts by status.
// GET /internal/v1/outbox/status
func (h *Handler) handleStatus(c *gin.Context) {
	if h.port == nil {
		apiutil.Error(c, 503, "outbox events repository not wired")
		return
	}

	statuses := []string{"pending", "processing", "completed", "dead_letter", "superseded"}
	counts := make(map[string]int64, len(statuses))

	for _, status := range statuses {
		count, err := h.port.CountByStatus(c.Request.Context(), status)
		if err != nil {
			h.log.Error("failed to count outbox status",
				zap.String("status", status), zap.Error(err))
			apiutil.InternalError(c, err)
			return
		}
		counts[status] = count
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"counts": counts,
	})
}

// handleEvents lists pending and processing outbox events.
// GET /internal/v1/outbox/events
func (h *Handler) handleEvents(c *gin.Context) {
	if h.port == nil {
		apiutil.Error(c, 503, "outbox events repository not wired")
		return
	}

	events, err := h.port.ListPending(c.Request.Context())
	if err != nil {
		h.log.Error("failed to list pending outbox events", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	if events == nil {
		events = []outbox.EventDTO{}
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"events": events,
		"count":  len(events),
	})
}
