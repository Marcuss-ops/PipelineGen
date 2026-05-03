package drive

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/service/drivecleanup"
	"velox/go-master/internal/service/drivereconcile"
	"velox/go-master/pkg/apiutil"
)

type Handler struct {
	cleanupSvc    *drivecleanup.Service
	reconcileSvc *drivereconcile.Service
}

func NewHandler(cleanupSvc *drivecleanup.Service, reconcileSvc *drivereconcile.Service) *Handler {
	return &Handler{cleanupSvc: cleanupSvc, reconcileSvc: reconcileSvc}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	zap.L().Info("RegisterRoutes called", zap.String("handler_addr", fmt.Sprintf("%p", h)))
	r.POST("/clip/:id/trash", h.TrashClip)
	r.POST("/clip/:id/delete", h.DeleteClip)
	r.POST("/reconcile", h.Reconcile)
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

// Reconcile checks for mismatches between SQLite and Google Drive.
// Body: { "source": "artlist", "root_folder_id": "xxx", "dry_run": true }
func (h *Handler) Reconcile(c *gin.Context) {
	if h.reconcileSvc == nil {
		apiutil.InternalError(c, fmt.Errorf("reconcile service not configured"))
		return
	}

	var req struct {
		Source       string `json:"source"`
		RootFolderID string `json:"root_folder_id"`
		DryRun       bool   `json:"dry_run"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request body")
		return
	}

	ctx := c.Request.Context()
	result, err := h.reconcileSvc.Reconcile(ctx, req.Source, req.RootFolderID, req.DryRun)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, result)
}
