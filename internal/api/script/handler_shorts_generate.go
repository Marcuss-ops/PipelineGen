// Package script — handler_shorts_generate.go owns POST /shorts/generate.
//
// PR-SHORTS-EXTRACT (July 2026): extracted from HandlerGenerate so the
// generation handler owns only script-generation submission.
package script

import (
	"net/http"

	"github.com/gin-gonic/gin"

	shorts "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/shorts"
)

// GenerateShorts builds a deterministic Remotion Shorts payload. It is
// intentionally separate from /generate: no LLM call, script regeneration,
// asset selection or sound-effect synthesis happens here. The caller sends
// the approved text, clip references and (optionally) already-indexed SFX.
// include_sound_effects=false always returns sound_effects: [].
func (h *HandlerShorts) GenerateShorts(c *gin.Context) {
	var req shorts.Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid shorts payload: " + err.Error()})
		return
	}
	result, err := shorts.Build(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "shorts": result})
}
