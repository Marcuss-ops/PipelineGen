package media

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/core/media"
)

// Handler handles media-related HTTP requests.
type Handler struct {
	exporter *media.ManifestExporter
}

// NewHandler creates a new media handler.
func NewHandler(exporter *media.ManifestExporter) *Handler {
	return &Handler{exporter: exporter}
}

// RegisterRoutes registers the media routes.
func (h *Handler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/manifest/export", h.ExportManifest)
}

// ExportManifest handles GET /api/media/manifest/export.
func (h *Handler) ExportManifest(c *gin.Context) {
	workspaceID := c.Query("workspace_id")
	projectID := c.Query("project_id")

	if workspaceID == "" {
		workspaceID = "default"
	}

	manifest, err := h.exporter.Export(c.Request.Context(), workspaceID, projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, manifest)
}
