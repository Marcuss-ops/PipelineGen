package generation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	appgeneration "github.com/Marcuss-ops/PipelineGen/internal/application/generation"
	domaingeneration "github.com/Marcuss-ops/PipelineGen/internal/domain/generation"
)

// Handler exposes the unified generation API.
type Handler struct {
	svc service
	log *zap.Logger
}

type service interface {
	Create(ctx context.Context, req domaingeneration.Request) (*appgeneration.CreateResponse, error)
	Status(ctx context.Context, id string) (*appgeneration.StatusResponse, error)
	Cancel(ctx context.Context, id string) error
}

// NewHandler constructs a new generation handler.
func NewHandler(svc service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes mounts the unified generation routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("", h.Create)
	r.GET("/:id", h.Get)
	r.POST("/:id/cancel", h.Cancel)
}

type createRequest struct {
	SchemaVersion int                   `json:"schema_version"`
	Type          domaingeneration.Type `json:"type"`
	Input         json.RawMessage       `json:"input"`
	Options       map[string]any        `json:"options,omitempty"`
}

// Create enqueues a generation job.
func (h *Handler) Create(c *gin.Context) {
	if h.svc == nil {
		api.Error(c, http.StatusServiceUnavailable, "generation service not initialized")
		return
	}
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}
	resp, err := h.svc.Create(c.Request.Context(), domaingeneration.Request{
		SchemaVersion: req.SchemaVersion,
		Type:          req.Type,
		Input:         req.Input,
		Options:       req.Options,
	})
	if err != nil {
		h.writeErr(c, err)
		return
	}
	api.Accepted(c, resp)
}

// Get returns the current job status.
func (h *Handler) Get(c *gin.Context) {
	if h.svc == nil {
		api.Error(c, http.StatusServiceUnavailable, "generation service not initialized")
		return
	}
	resp, err := h.svc.Status(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if err != nil {
		h.writeErr(c, err)
		return
	}
	api.OK(c, resp)
}

// Cancel cancels a generation job.
func (h *Handler) Cancel(c *gin.Context) {
	if h.svc == nil {
		api.Error(c, http.StatusServiceUnavailable, "generation service not initialized")
		return
	}
	if err := h.svc.Cancel(c.Request.Context(), strings.TrimSpace(c.Param("id"))); err != nil {
		h.writeErr(c, err)
		return
	}
	api.OK(c, gin.H{"ok": true})
}

func (h *Handler) writeErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, appgeneration.ErrUnsupportedType):
		api.BadRequest(c, err.Error())
	case errors.Is(err, appgeneration.ErrTypeDisabled):
		api.Error(c, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, appgeneration.ErrJobNotFound):
		api.NotFound(c, err.Error())
	default:
		if h.log != nil {
			h.log.Error("generation request failed", zap.Error(err))
		}
		api.InternalError(c, err)
	}
}
