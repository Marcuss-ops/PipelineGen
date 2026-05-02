package voiceover

import (
	"fmt"
	"strings"

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
	r.POST("/sync", h.Sync)
	r.GET("/sync/status", h.Status)
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

func (h *SyncHandler) Status(c *gin.Context) {
	rootID := strings.TrimSpace(c.Query("root_id"))
	if rootID == "" {
		apiutil.BadRequest(c, "root_id query parameter is required")
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"root_id": rootID,
		"message": "Voiceover sync status endpoint - use POST /sync to trigger sync",
	})
}
