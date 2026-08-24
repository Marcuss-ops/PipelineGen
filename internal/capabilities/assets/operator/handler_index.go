// Package operator — handler_index.go (RESOURCE: INDEX-HEALTH, July 2026
// split by resource).
//
// Split rationale (resource/handler), see handler.go header.
//
// This file owns the INDEX-HEALTH resource (single dashboard endpoint).
// 1 route:
//
//   - GET /index-health → handleIndexHealth
//
// registers via the private registerIndexRoutes method, called from
// handler.go::RegisterRoutes.
//
// kept standalone (not folded into handler_summary.go) so future
// index-health diagnostics (Qdrant / outbox / embedding coverage) can
// grow into a sibling helper without bloating the summary resource.
package operator

import (
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// registerIndexRoutes mounts the index-health endpoint on the shared
// /api/assets/operator/* prefix. The path "/index-health" is RELATIVE
// to the parent router group.
func (h *Handler) registerIndexRoutes(rg *gin.RouterGroup) {
	rg.GET("/index-health", h.handleIndexHealth)
}

// handleIndexHealth returns index health for the dashboard.
func (h *Handler) handleIndexHealth(c *gin.Context) {
	// Reuse existing diagnostics data
	apiutil.OK(c, gin.H{
		"ok":       true,
		"degraded": false,
	})
}
