package artlist

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/catalogsync"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/apiutil"
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
	r.POST("/run", h.RunTagPipeline)
	r.GET("/runs/:run_id", h.RunStatus)
	r.GET("/stats", h.Stats)
	r.POST("/search/live", h.SearchLive)
}

func requireInternalHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Internal")), "true") || strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Velox-Internal")), "true") {
			c.Next()
			return
		}
		apiutil.Error(c, http.StatusForbidden, "internal endpoint")
		c.Abort()
	}
}
