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
package assets

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

// handleOutboxEvents lists outbox events. By default it returns
// pending/processing events; pass ?status= to filter by a specific
// status bucket (e.g. dead_letter, completed, failed).
func (h *Handler) handleOutboxEvents(c *gin.Context) {
	if h.outboxPort == nil {
		apiutil.OK(c, gin.H{"ok": true, "events": []any{}, "count": 0})
		return
	}

	ctx := c.Request.Context()
	var events []any

	if status := c.Query("status"); status != "" {
		dtos, listErr := h.outboxPort.ListByStatus(ctx, status)
		if listErr != nil {
			h.log.Error("failed to list outbox events by status", zap.String("status", status), zap.Error(listErr))
			apiutil.InternalError(c, listErr)
			return
		}
		events = make([]any, len(dtos))
		for i, e := range dtos {
			events[i] = e
		}
	} else {
		dtos, listErr := h.outboxPort.ListPending(ctx)
		if listErr != nil {
			h.log.Error("failed to list outbox events", zap.Error(listErr))
			apiutil.InternalError(c, listErr)
			return
		}
		events = make([]any, len(dtos))
		for i, e := range dtos {
			events[i] = e
		}
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"events": events,
		"count":  len(events),
	})
}
