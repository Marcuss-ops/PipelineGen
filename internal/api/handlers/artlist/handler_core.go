package artlist

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/service/artlist"
)

type Handler struct {
	service        *artlist.Service
	nodeScraperDir string
	log            *zap.Logger
}

func NewHandler(
	service *artlist.Service,
	nodeScraperDir string,
	log *zap.Logger,
) *Handler {
	return &Handler{
		service:        service,
		nodeScraperDir: nodeScraperDir,
		log:            log,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering Artlist routes")
	r.POST("/run", h.RunTagPipeline)
	r.GET("/runs/:run_id", h.RunStatus)
	r.GET("/diagnostics", h.Diagnostics)
	r.POST("/search/live", h.SearchLive)

	internal := r.Group("")
	internal.Use(requireInternalHeader())
	{
		internal.GET("/stats", h.Stats)
		internal.POST("/search", h.Search)
		internal.POST("/sync-drive-folder", h.SyncDriveFolder)
		internal.POST("/import-scraper-db", h.ImportScraperDB)
		internal.GET("/test", func(c *gin.Context) {
			c.JSON(200, gin.H{"ok": true, "message": "test endpoint works"})
		})

		// Clip lifecycle endpoints
		internal.GET("/clips/:id/status", h.GetClipStatus)
		internal.POST("/clips/:id/download", h.DownloadClip)
		internal.POST("/clips/:id/upload-drive", h.UploadClipToDrive)
		internal.POST("/clips/process", h.ProcessClip)
	}
}

func requireInternalHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Internal")), "true") || strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Velox-Internal")), "true") {
			c.Next()
			return
		}
		c.JSON(http.StatusForbidden, gin.H{
			"ok":    false,
			"error": "internal endpoint",
		})
		c.Abort()
	}
}
