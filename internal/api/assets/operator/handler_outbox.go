// Package operator — handler_outbox.go (RESOURCE: OUTBOX, July 2026
// split by resource).
//
// Split rationale (resource/handler), see handler.go header.
//
// This file owns the OUTBOX resource (status + events). 2 routes:
//
//   - GET /outbox/status  → handleOutboxStatus
//   - GET /outbox/events  → handleOutboxEvents
//
// registers via the private registerOutboxRoutes method, called from
// handler.go::RegisterRoutes.
//
// No cross-resource helpers needed by this file.
package operator

import (
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// registerOutboxRoutes mounts outbox transports on the shared
// /api/assets/operator/* prefix. The paths "/outbox/status" +
// "/outbox/events" are RELATIVE to the parent router group.
func (h *Handler) registerOutboxRoutes(rg *gin.RouterGroup) {
	rg.GET("/outbox/status", h.handleOutboxStatus)
	rg.GET("/outbox/events", h.handleOutboxEvents)
}

// handleOutboxStatus returns outbox event counts by status.
// godlike/07 NO-FAKE-AVAILABILITY: when outboxPort is nil (no outbox
// capability at composition time), returns 200 with empty counts map
// (not 503) so the admin dashboard degrades gracefully.
func (h *Handler) handleOutboxStatus(c *gin.Context) {
	if h.outboxPort == nil {
		apiutil.OK(c, gin.H{"ok": true, "counts": map[string]int64{}})
		return
	}

	ctx := c.Request.Context()
	statuses := []string{"pending", "processing", "completed", "dead_letter", "superseded"}
	counts := make(map[string]int64, len(statuses))

	for _, status := range statuses {
		count, err := h.outboxPort.CountByStatus(ctx, status)
		if err != nil {
			h.log.Warn("failed to count outbox status", zap.String("status", status), zap.Error(err))
			continue
		}
		counts[status] = count
	}

	apiutil.OK(c, gin.H{"ok": true, "counts": counts})
}

// handleOutboxEvents lists pending and processing outbox events.
func (h *Handler) handleOutboxEvents(c *gin.Context) {
	if h.outboxPort == nil {
		apiutil.OK(c, gin.H{"ok": true, "events": []any{}, "count": 0})
		return
	}

	events, err := h.outboxPort.ListPending(c.Request.Context())
	if err != nil {
		h.log.Error("failed to list outbox events", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"events": events,
		"count":  len(events),
	})
}
