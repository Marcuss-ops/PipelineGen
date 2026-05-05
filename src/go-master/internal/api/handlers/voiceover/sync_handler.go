package voiceover

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/service/voiceoversync"
	"velox/go-master/pkg/apiutil"
)

type SyncHandler struct {
	syncService *voiceoversync.Service
	log         *zap.Logger
}

func NewSyncHandler(syncService *voiceoversync.Service, log *zap.Logger) *SyncHandler {
	return &SyncHandler{
		syncService: syncService,
		log:         log,
	}
}

func (h *SyncHandler) RegisterRoutes(r *gin.RouterGroup) {
	// Don't register routes if service is nil
	if h.syncService == nil {
		h.log.Warn("voiceover sync service is nil, skipping route registration")
		return
	}
	r.POST("/sync", h.Sync)
	// Note: /sync/status removed - was returning fake status (debt)
	// To re-add, implement real status with: last sync time, running state, etc.
}

func (h *SyncHandler) Sync(c *gin.Context) {
	if h.syncService == nil {
		apiutil.InternalError(c, fmt.Errorf("voiceover sync service not configured"))
		return
	}

	h.log.Info("starting voiceover sync")

	summary, err := h.syncService.Sync(c.Request.Context())
	if err != nil {
		h.log.Error("voiceover sync failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, summary)
}
