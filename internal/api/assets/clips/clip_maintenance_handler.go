// Package clips — clip maintenance sub-handler (Fase 2 split, June 2026).
//
// Extracted from ops.go: clip deletion operations (TrashClip, DeleteClip).
// Depends on: DeletionSvc.
package clips

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// deleteClip is the shared body for both Trash and Delete endpoints.
func (oh *OpsHandler) deleteClip(c *gin.Context, hardDelete bool) {
	source := c.Param("source")
	clipID := c.Param("id")
	action := "trashed"
	if hardDelete {
		action = "deleted"
	}
	if err := oh.deletionSvc.DeleteClip(c.Request.Context(), source, clipID, hardDelete); err != nil {
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
func (oh *OpsHandler) TrashClip(c *gin.Context) { oh.deleteClip(c, false) }

// DeleteClip permanently deletes a clip from Drive and SQLite.
func (oh *OpsHandler) DeleteClip(c *gin.Context) { oh.deleteClip(c, true) }
