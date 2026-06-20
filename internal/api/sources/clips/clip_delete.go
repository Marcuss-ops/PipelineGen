// Package clips — clip trash/delete endpoints (PR-A Phase 4 BULK:
// methods retargeted from *DeleteHandler to *Handler).
package clips

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// deleteClip is the shared body for both Trash and Delete endpoints.
// hardDelete=false mirrors TrashClip; hardDelete=true mirrors DeleteClip.
func (h *Handler) deleteClip(c *gin.Context, hardDelete bool) {
	source := c.Param("source")
	clipID := c.Param("id")
	action := "trashed"
	if hardDelete {
		action = "deleted"
	}
	if err := h.deletionSvc.DeleteClip(c.Request.Context(), source, clipID, hardDelete); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  action,
		"source":  source,
		"clip_id": clipID,
	})
}

// TrashClip moves a clip to Drive trash and removes SQLite record.
//   - POST /:source/clips/:id/trash
func (h *Handler) TrashClip(c *gin.Context) { h.deleteClip(c, false) }

// DeleteClip permanently deletes a clip from Drive and SQLite.
//   - POST /:source/clips/:id/delete
func (h *Handler) DeleteClip(c *gin.Context) { h.deleteClip(c, true) }
