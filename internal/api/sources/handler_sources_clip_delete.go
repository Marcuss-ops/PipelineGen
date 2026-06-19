package sources

import (
	"github.com/gin-gonic/gin"
)

// TrashClip moves a clip to Drive trash and removes SQLite record.
func (h *Handler) TrashClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	if err := h.deletionSvc.DeleteClip(c.Request.Context(), source, clipID, false); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "trashed",
		"source":  source,
		"clip_id": clipID,
	})
}

// DeleteClip permanently deletes a clip from Drive and SQLite.
func (h *Handler) DeleteClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	if err := h.deletionSvc.DeleteClip(c.Request.Context(), source, clipID, true); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "deleted",
		"source":  source,
		"clip_id": clipID,
	})
}
