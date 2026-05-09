package assettree

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/service/assettree"
	"velox/go-master/pkg/apiutil"
)

// Handler handles HTTP requests for the asset tree.
type Handler struct {
	service *assettree.Service
	log     *zap.Logger
}

// NewHandler creates a new asset tree HTTP handler.
func NewHandler(service *assettree.Service, log *zap.Logger) *Handler {
	return &Handler{
		service: service,
		log:     log,
	}
}

// RegisterRoutes registers the asset tree routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	// e.g., /api/assets/:source/tree?parent_id=...
	r.GET("/:source/tree", h.GetTree)
	// e.g., /api/assets/:source/breadcrumb?id=...
	r.GET("/:source/breadcrumb", h.GetBreadcrumb)
}

// GetTree returns the direct children of a given parent folder.
// If parent_id is missing or "root", it returns the root folders for that source.
func (h *Handler) GetTree(c *gin.Context) {
	source := c.Param("source")
	parentID := c.Query("parent_id")

	if parentID == "root" {
		parentID = ""
	}

	children, err := h.service.ListChildren(c.Request.Context(), source, parentID)
	if err != nil {
		h.log.Error("failed to list children", zap.Error(err), zap.String("source", source), zap.String("parent_id", parentID))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":       true,
		"source":   source,
		"children": children,
	})
}

// GetBreadcrumb returns the path from root down to the specified node ID.
func (h *Handler) GetBreadcrumb(c *gin.Context) {
	source := c.Param("source")
	id := c.Query("id")

	if id == "" {
		apiutil.BadRequest(c, "missing id parameter")
		return
	}

	breadcrumb, err := h.service.GetBreadcrumb(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to get breadcrumb", zap.Error(err), zap.String("source", source), zap.String("id", id))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"breadcrumb": breadcrumb,
	})
}
