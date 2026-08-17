package cliprender

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler is the thin HTTP transport for POST /api/clips/render. It
// owns request binding, validation, and Master enqueue; all business
// logic lives in the cliprender capability (and, in the follow-up
// steps, its worker).
type Handler struct {
	jobsSvc job.Service
	log     *zap.Logger

	// Idempotency is the reusable Gin idempotency middleware applied
	// to POST /clips/render. nil disables (test fixtures).
	Idempotency gin.HandlerFunc
}

// NewHandler constructs the thin handler. jobsSvc is mandatory.
func NewHandler(jobsSvc job.Service, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{jobsSvc: jobsSvc, log: log}
}

// RegisterRoutes mounts the clip.render surface under the /clips
// router group (production path /api/clips/render).
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	idem := h.Idempotency
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}
	r.POST("/render", idem, h.Render)
}

// Render handles POST /api/clips/render.
//
// Validation chain (godlike/07 fail-fast):
//  1. Strict JSON decode with DisallowUnknownFields → UNKNOWN_FIELD or
//     INVALID_PAYLOAD on syntax error.
//  2. Normalize + Validate the canonical RenderRequest → INVALID_PAYLOAD.
//  3. Enqueue a canonical clip.render Master job with the normalized
//     request as the payload → 202 {job_id, status: "QUEUED"}.
//
// Errors:
//   - jobsSvc nil → 503 JOBS_UNAVAILABLE (fail-closed: never a
//     successful no-op).
//   - Enqueue failure → 500 (the Master is the canonical owner of the
//     queue-idempotency contract; no inline execution fallback).
func (h *Handler) Render(c *gin.Context) {
	var req RenderRequest

	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		code := ErrCodeInvalidPayload
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			code = ErrCodeUnknownField
		}
		c.JSON(http.StatusBadRequest, renderResponse{
			Status:    StatusError,
			Error:     "invalid JSON payload: " + err.Error(),
			ErrorCode: code,
		})
		return
	}

	req.Normalize()
	if err := req.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, renderResponse{
			Status:    StatusError,
			Error:     err.Error(),
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}

	if h.jobsSvc == nil {
		c.JSON(http.StatusServiceUnavailable, renderResponse{
			Status:    StatusError,
			Error:     "jobs service not configured",
			ErrorCode: ErrCodeJobsUnavailable,
		})
		return
	}

	enqueued, err := h.jobsSvc.Enqueue(c.Request.Context(), &job.EnqueueRequest{
		Type:    TypeClipRender,
		Payload: &req,
	})
	if err != nil {
		h.log.Error("clip.render enqueue failed",
			zap.String("source_asset_id", req.SourceAssetID),
			zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	h.log.Info("clip.render enqueued",
		zap.String("job_id", enqueued.ID),
		zap.String("source_asset_id", req.SourceAssetID),
		zap.String("background_mode", req.Background.Mode),
		zap.Bool("watermark", req.Watermark.Enabled),
		zap.Bool("subtitles", req.Subtitles.Enabled),
		zap.String("output_contract", req.Output.Contract),
	)

	apiutil.Accepted(c, renderResponse{
		JobID:  enqueued.ID,
		Status: StatusQueued,
	})
}
