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
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/diagnostics", h.Diagnostics)
	r.GET("/index-health", h.IndexHealth)
	r.GET("/qdrant/health", h.QdrantHealth)
	r.POST("/qdrant/cleanup", h.QdrantCleanup)
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
		"ok":            result.OK,
		"degraded":      result.Degraded,
		"index_health":  indexHealth,
		"asset_stats":   assetStats,
	})
}

// ── QdrantHealth (GET /qdrant/health) ─────────────────────────────

func (h *Handler) QdrantHealth(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "diagnostics service not wired")
		return
	}
	result, err := h.svc.Check(c.Request.Context(), appdiag.HealthCommand{})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	ih, _ := result.Checks["index_health"].(map[string]any)
	qdrantHealthy := false
	if ih != nil {
		qdrantHealthy, _ = ih["qdrant_healthy"].(bool)
	}
	enabled := ih != nil
	apiutil.OK(c, gin.H{
		"ok":      result.OK,
		"healthy": qdrantHealthy,
		"enabled": enabled,
	})
}

// ── QdrantCleanup (POST /qdrant/cleanup) ──────────────────────────

// QdrantCleanup is a public no-op placeholder. The real cleanup runs
// periodically via background_jobs.go (startQdrantCleaner) and is
// triggered by the admin CLI, not by this public endpoint.
// Kept for route backward compatibility during the migration.
func (h *Handler) QdrantCleanup(c *gin.Context) {
	apiutil.OK(c, gin.H{
		"ok":      true,
		"message": "Qdrant stale-link cleaner runs every 12h automatically. Use the admin CLI for manual triggers.",
	})
}
