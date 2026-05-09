package assets

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes registers all asset-related routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	// Search & Stats
	r.GET("/search", h.Search)
	r.GET("/stats", h.Stats)

	// Tree & Breadcrumb
	r.GET("/:source/tree", h.GetTree)
	r.GET("/:source/breadcrumb", h.GetBreadcrumb)

	// Folder-level endpoints (Migrated from media)
	r.GET("/:source/folders", h.ListFolders)
	r.GET("/:source/folders/:id/children", h.GetFolderChildren)
	r.GET("/:source/folders/:id/status", h.FolderStatus)
	r.POST("/:source/folders/:id/regenerate-manifest", h.RegenerateManifest)
	r.POST("/:source/folders/:id/trash", h.TrashFolder)
	r.POST("/:source/folders/:id/delete", h.DeleteFolder)
}
