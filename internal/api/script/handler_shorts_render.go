// Package script — handler_shorts_render.go owns POST /shorts/render.
//
// PR-SHORTS-EXTRACT (July 2026): extracted from HandlerGenerate so the
// generation handler owns only script-generation submission.
package script

import (
	"net/http"

	"github.com/gin-gonic/gin"

	shorts "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/shorts"
)

// RenderShorts builds the Shorts plan and synchronously asks Remotion to
// render it. The request remains the same as /shorts/generate, with optional
// fps, width and height fields. The asynchronous job producer remains
// available for the next worker-based iteration.
func (h *HandlerShorts) RenderShorts(c *gin.Context) {
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
	if h == nil || h.renderer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "Remotion renderer is not configured"})
		return
	}
	renderJob, err := shorts.BuildRenderJob(req, plan)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	rendered, err := h.renderer.Render(c.Request.Context(), renderJob)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": "Remotion render failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "shorts": plan, "render": rendered})
}
