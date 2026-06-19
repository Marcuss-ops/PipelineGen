// Package clips hosts the HTTP handlers for the clip lifecycle endpoints
// that were previously part of the flat api/sources/ package. PR-A Phase 4.
// Each sub-handler in this package owns a small surface area of the clip
// CRUD: delete (Phase 4-minimum), create (future), enrich (future), etc.
package clips

import (
	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/api/sources/internal"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
)

// DeleteHandler owns the clip-trash and clip-delete endpoints.
// Phase 4 minimum: only relies on the deletionSvc so wiring stays tiny.
// Future Phase 4 subphases will add CreateHandler, EnrichHandler, etc.
//
// Methods are extracted from the legacy flat handler_sources_clip_delete.go
// — same behavior, same routes, just a fresh receiver and subpackage.
type DeleteHandler struct {
	deletionSvc *media.DeletionService
}

// NewDeleteHandler builds the DeleteHandler.
//
//	deletionSvc - canonical DeletionService shared with the rest of the
//	              clip handler chain. May be nil only if the caller is
//	              guaranteed to disable the trash/delete routes.
func NewDeleteHandler(deletionSvc *media.DeletionService) *DeleteHandler {
	return &DeleteHandler{deletionSvc: deletionSvc}
}

// deleteClip is the shared body for both Trash and Delete endpoints.
// hardDelete=false mirrors TrashClip; hardDelete=true mirrors DeleteClip.
func (h *DeleteHandler) deleteClip(c *gin.Context, hardDelete bool) {
	source := c.Param("source")
	clipID := c.Param("id")
	action := "trashed"
	if hardDelete {
		action = "deleted"
	}
	if err := h.deletionSvc.DeleteClip(c.Request.Context(), source, clipID, hardDelete); err != nil {
		internal.APIUtil.InternalError(c, err)
		return
	}
	internal.APIUtil.OK(c, gin.H{
		"ok":      true,
		"action":  action,
		"source":  source,
		"clip_id": clipID,
	})
}

// TrashClip moves a clip to Drive trash and removes SQLite record.
//   - POST /:source/clips/:id/trash
func (h *DeleteHandler) TrashClip(c *gin.Context) { h.deleteClip(c, false) }

// DeleteClip permanently deletes a clip from Drive and SQLite.
//   - POST /:source/clips/:id/delete
func (h *DeleteHandler) DeleteClip(c *gin.Context) { h.deleteClip(c, true) }

// RegisterRoutes mounts TrashClip + DeleteClip onto the supplied gin
// router group. SourcesHandler delegates these two endpoints to here
// so the methods live in package clips rather than the flat sources
// root. Path patterns mirror SourcesHandler's legacy routes exactly.
func (h *DeleteHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/:source/clips/:id/trash", h.TrashClip)
	r.POST("/:source/clips/:id/delete", h.DeleteClip)
}
