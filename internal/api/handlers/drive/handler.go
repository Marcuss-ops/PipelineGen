package drive

import (
	"github.com/gin-gonic/gin"
	"velox/go-master/internal/service/drivecleanup"
	"velox/go-master/pkg/apiutil"
)

type Handler struct {
	cleanupSvc *drivecleanup.Service
}

func NewHandler(cleanupSvc *drivecleanup.Service) *Handler {
	return &Handler{cleanupSvc: cleanupSvc}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/clip/:id/trash", h.TrashClip)
	r.POST("/clip/:id/delete", h.DeleteClip)
}

type ClipActionRequest struct {
	DryRun bool `json:"dry_run"`
}

// TrashClip moves a file to Drive trash and removes the SQLite record.
func (h *Handler) TrashClip(c *gin.Context) {
	clipID := c.Param("id")
	if clipID == "" {
		apiutil.BadRequest(c, "clip id is required")
		return
	}

	ctx := c.Request.Context()
	if err := h.cleanupSvc.TrashClip(ctx, clipID); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "trashed",
		"clip_id": clipID,
	})
}

// DeleteClip permanently deletes a file from Drive and removes the SQLite record.
func (h *Handler) DeleteClip(c *gin.Context) {
	clipID := c.Param("id")
	if clipID == "" {
		apiutil.BadRequest(c, "clip id is required")
		return
	}

	ctx := c.Request.Context()
	if err := h.cleanupSvc.DeleteClipPermanently(ctx, clipID); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "deleted",
		"clip_id": clipID,
	})
}

// TODO: Add reconcile endpoint
// POST /api/drive/reconcile
// Body: { "source": "artlist", "dry_run": true }
