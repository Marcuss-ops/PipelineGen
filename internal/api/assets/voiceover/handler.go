// Package voiceover provides the single canonical HTTP handler for
// voiceover generation. All voiceover requests go through
// POST /api/media/voiceovers.
//
// PR 4 (June 2026): consolidated 6 endpoints (/generate, /generate-with-group,
// /batch, /promo, /sync, GET /groups) into a single always-async endpoint.
// Legacy endpoints respond 410 Gone.
package voiceover

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler is the single voiceover HTTP handler.
// POST /api/media/voiceovers — enqueues a voiceover.generate job (always async).
type Handler struct {
	service *voiceover.Service
	jobsSvc jobservice.Service
	log     *zap.Logger
}

// NewHandler builds the handler. service and jobsSvc are required.
func NewHandler(
	service *voiceover.Service,
	jobsSvc jobservice.Service,
	log *zap.Logger,
) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		service: service,
		jobsSvc: jobsSvc,
		log:     log,
	}
}

// RegisterRoutes registers the single voiceover endpoint and 410 Gone
// stubs for the six legacy paths.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	// ── Canonical endpoint ──
	r.POST("/", h.Generate)

	// ── Legacy stubs (410 Gone) ──
	gone := h.goneLegacy
	r.POST("/generate", gone)
	r.POST("/generate-with-group", gone)
	r.POST("/batch", gone)
	r.POST("/promo", gone)
	r.POST("/sync", gone)
	r.GET("/groups", gone)
}

// Generate enqueues a voiceover.generate job and returns the job_id.
//
//	POST /api/media/voiceovers
//	Body: domain.GenerateVoiceoverCommand (JSON)
//	Response: { "ok": true, "job_id": "<id>" }
//
// The endpoint is always asynchronous — there is no sync path.
func (h *Handler) Generate(c *gin.Context) {
	if h.service == nil {
		apiutil.BadRequest(c, "voiceover service not initialized")
		return
	}
	if h.jobsSvc == nil {
		apiutil.InternalError(c, fmt.Errorf("job system not available"))
		return
	}

	cmd, ok := apiutil.BindJSON[domain.GenerateVoiceoverCommand](c)
	if !ok {
		return
	}

	if err := cmd.Validate(); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	h.log.Info("enqueuing voiceover.generate job",
		zap.String("locale", string(cmd.Locale.Normalize())),
		zap.Bool("force_regenerate", cmd.ForceRegenerate))

	enqueued, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:    appjobs.TypeVoiceoverGenerate,
		Payload: cmd,
	})
	if err != nil {
		h.log.Error("voiceover enqueue failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"job_id": enqueued.ID,
	})
}

// goneLegacy responds 410 Gone with the new endpoint name.
func (h *Handler) goneLegacy(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{
		"error":   "Gone",
		"message": "This endpoint has been removed. Use POST /api/media/voiceovers instead.",
	})
}
