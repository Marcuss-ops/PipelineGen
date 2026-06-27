// Package diagnostics provides HTTP transport for system diagnostics
// (index-health, qdrant health, qdrant cleanup). All business logic
// is delegated to the application/assets/diagnostics.Service.
package diagnostics

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appdiag "github.com/Marcuss-ops/PipelineGen/internal/application/assets/diagnostics"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler is the thin HTTP transport for diagnostics operations.
type Handler struct {
	svc *appdiag.Service
	log *zap.Logger
}

// NewHandler creates a DiagnosticsHandler.
func NewHandler(svc *appdiag.Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes registers diagnostics routes under the given group.
// PR7 (June 2026): /qdrant/health removed — consolidated into GET /health?check=qdrant.
// PR1 (June 2026): /qdrant/cleanup removed — was a no-op placeholder; the real
// periodic cleanup runs via background_jobs.go (startQdrantCleaner) and the
// admin CLI, not via this public endpoint.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/diagnostics", h.Diagnostics)
	r.GET("/index-health", h.IndexHealth)
}

// ── Diagnostics (GET /diagnostics) ─────────────────────────────────

func (h *Handler) Diagnostics(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "diagnostics service not wired")
		return
	}
	result, err := h.svc.Check(c.Request.Context(), appdiag.HealthCommand{})
	if err != nil {
		h.log.Error("diagnostics check failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{
		"ok":       result.OK,
		"degraded": result.Degraded,
		"checks":   result.Checks,
	})
}

// ── IndexHealth (GET /index-health) ───────────────────────────────

func (h *Handler) IndexHealth(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "diagnostics service not wired")
		return
	}
	result, err := h.svc.Check(c.Request.Context(), appdiag.HealthCommand{})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	// Surface the index_health sub-check prominently for backwards compat.
	indexHealth, _ := result.Checks["index_health"]
	assetStats, _ := result.Checks["asset_stats"]
	c.JSON(http.StatusOK, gin.H{
		"ok":           result.OK,
		"degraded":     result.Degraded,
		"index_health": indexHealth,
		"asset_stats":  assetStats,
	})
}

