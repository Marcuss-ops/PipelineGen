package artlist

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/api/middleware"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/catalogsync"
	jobservice "velox/go-master/internal/service/jobs"
)

type Handler struct {
	service        *artlist.Service
	catalogSync    *catalogsync.Service
	jobsService    *jobservice.Service
	nodeScraperDir string
	log            *zap.Logger
}

func NewHandler(
	service *artlist.Service,
	catalogSync *catalogsync.Service,
	jobsService *jobservice.Service,
	nodeScraperDir string,
	log *zap.Logger,
) *Handler {
	return &Handler{
		service:        service,
		catalogSync:    catalogSync,
		jobsService:    jobsService,
		nodeScraperDir: nodeScraperDir,
		log:            log,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering Artlist routes")

	// Public routes
	r.POST("/run", h.RunTagPipeline)
	r.POST("/run-smart", h.RunSmartPipeline)
	r.GET("/runs/:run_id", h.RunStatus)
	r.GET("/stats", h.Stats)
	r.GET("/diagnostics", h.Diagnostics)

	// Internal routes (require X-Internal or X-Velox-Internal header)
	internal := r.Group("", middleware.RequireInternalHeader())
	internal.POST("/search", h.Search)
	internal.POST("/search/live", h.SearchLive)
	internal.POST("/recommend", h.Recommend)
	internal.POST("/sync-catalogs", h.SyncCatalogs)
}
