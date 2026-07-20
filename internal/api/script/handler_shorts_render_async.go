// Package script — handler_shorts_render_async.go owns POST /shorts/render/async.
//
// PR-SHORTS-EXTRACT (July 2026): extracted from HandlerGenerate so the
// generation handler owns only script-generation submission.
package script

import (
	"net/http"

	"github.com/gin-gonic/gin"

	shorts "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/shorts"
)

// RenderShortsAsync creates a render.video job and returns immediately. The
// registered video job handler performs the Remotion HTTP call in a worker.
func (h *HandlerShorts) RenderShortsAsync(c *gin.Context) {
	var req shorts.Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid shorts render payload: " + err.Error()})
		return
	}
	plan, err := shorts.Build(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if h == nil || h.producer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "Remotion render producer is not configured"})
		return
	}
	renderJob, err := shorts.BuildRenderJob(req, plan)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	queued, err := h.producer.Enqueue(c.Request.Context(), renderJob)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "could not enqueue Remotion render: " + err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "job_id": queued.ID, "status": "QUEUED", "shorts": plan})
}
