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
	cleanupSvc   *drivecleanup.Service
	reconcileSvc *drivereconcile.Service
}

func NewHandler(cleanupSvc *drivecleanup.Service, reconcileSvc *drivereconcile.Service) *Handler {
	return &Handler{cleanupSvc: cleanupSvc, reconcileSvc: reconcileSvc}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	zap.L().Info("RegisterRoutes called", zap.String("handler_addr", fmt.Sprintf("%p", h)))
	r.POST("/reconcile", h.Reconcile)
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
