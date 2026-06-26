// Package outbox provides thin HTTP transport for the outbox events system
// (QDRANT-002). The handler exposes operator-facing read-only endpoints at
// /internal/v1/outbox for monitoring outbox health and listing pending events.
//
// This is the canonical internal outbox endpoint — the final gap from
// QDRANT-002 ("l'endpoint interno canonico non è montato nel server").
package outbox

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// Handler is the thin HTTP transport for outbox events monitoring.
type Handler struct {
	repo *outboxevents.Repository
	log  *zap.Logger
}

// NewHandler creates a new outbox events HTTP handler.
func NewHandler(repo *outboxevents.Repository, log *zap.Logger) *Handler {
	return &Handler{repo: repo, log: log}
}

// RegisterRoutes mounts the outbox endpoints under the given router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/status", h.handleStatus)
	rg.GET("/events", h.handleEvents)
}

// handleStatus returns aggregated outbox event counts by status.
// GET /internal/v1/outbox/status
func (h *Handler) handleStatus(c *gin.Context) {
	if h.repo == nil {
		api.Error(c, 503, "outbox events repository not wired")
		return
	}

	statuses := []string{"pending", "processing", "completed", "dead_letter", "superseded"}
	counts := make(map[string]int64, len(statuses))

	for _, status := range statuses {
		count, err := h.repo.CountByStatus(c.Request.Context(), status)
		if err != nil {
			h.log.Error("failed to count outbox status",
				zap.String("status", status), zap.Error(err))
			api.InternalError(c, err)
			return
		}
		counts[status] = count
	}

	api.OK(c, gin.H{
		"ok":     true,
		"counts": counts,
	})
}

// handleEvents lists pending and processing outbox events.
// GET /internal/v1/outbox/events
func (h *Handler) handleEvents(c *gin.Context) {
	if h.repo == nil {
		api.Error(c, 503, "outbox events repository not wired")
		return
	}

	events, err := h.repo.ListPending(c.Request.Context())
	if err != nil {
		h.log.Error("failed to list pending outbox events", zap.Error(err))
		api.InternalError(c, err)
		return
	}

	if events == nil {
		events = []outboxevents.Event{}
	}

	api.OK(c, gin.H{
		"ok":     true,
		"events": events,
		"count":  len(events),
	})
}
