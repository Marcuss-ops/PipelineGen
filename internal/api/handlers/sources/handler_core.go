package sources

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/api/middleware"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/catalogsync"
	"velox/go-master/internal/service/clipresolver"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/config"
)

type ArtlistHandler struct {
	service        *artlist.Service
	catalogSync    *catalogsync.Service
	jobsService    *jobservice.Service
	clipResolver   *clipresolver.Service
	nodeScraperDir string
	log            *zap.Logger
	presetsConfig  *artlist.PresetsConfig
	cfg            *config.Config
}

func NewArtlistHandler(
	service *artlist.Service,
	catalogSync *catalogsync.Service,
	jobsService *jobservice.Service,
	clipResolver *clipresolver.Service,
	nodeScraperDir string,
	log *zap.Logger,
	presetsConfig *artlist.PresetsConfig,
	cfg *config.Config,
) *ArtlistHandler {
	return &ArtlistHandler{
		service:        service,
		catalogSync:    catalogSync,
		jobsService:    jobsService,
		clipResolver:   clipResolver,
		nodeScraperDir: nodeScraperDir,
		log:            log,
		presetsConfig:  presetsConfig,
		cfg:            cfg,
	}
}

func (h *ArtlistHandler) RegisterRoutes(r *gin.RouterGroup) {
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
