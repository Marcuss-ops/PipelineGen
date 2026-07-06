// Package images (api/images) — sync_handler.go holds the manual
// sync handler (POST /api/images/sync). This triggers local
// filesystem + Drive synchronization for image assets.
//
// Territory: retrieved (operational, no AI generation).
package images

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// Sync handles POST /api/images/sync — manual filesystem + Drive
// synchronization. Runs local SyncAssets first, then SyncFromDrive.
func (h *ImagesHandler) Sync(c *gin.Context) {
	ctx := c.Request.Context()

	// 1. Local Sync
	if err := h.service.SyncAssets(); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// 2. Drive Sync
	if err := h.service.SyncFromDrive(ctx); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"message": "Synchronization complete (Local + Drive)"})
}
