package cliprender

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// MaxClipRenderRequestBytes bounds the POST /api/clips/render request body.
// It is deliberately generous (the canonical payload is a few KiB) but
// finite: a caller streaming an unbounded JSON document must fail at the
// transport with 413 rather than occupy a Master worker.
const MaxClipRenderRequestBytes int64 = 1 << 20 // 1 MiB

// decodeStrictBody owns the ONE strict-JSON decode used by every clip.render
// endpoint: the bounded body, the DisallowUnknownFields gate and the transport
// error mapping (413 PAYLOAD_TOO_LARGE, UNKNOWN_FIELD, INVALID_PAYLOAD). It
// writes the failure response itself and reports false so the caller only
// handles the success path.
func decodeStrictBody[T any](c *gin.Context, limit int64, bodyLabel string) (T, bool) {
	var req T
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, renderResponse{
				Status:    StatusError,
				Error:     fmt.Sprintf("%s body exceeds the %d byte limit", bodyLabel, limit),
				ErrorCode: ErrCodePayloadTooLarge,
			})
			return req, false
		}
		code := ErrCodeInvalidPayload
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			code = ErrCodeUnknownField
		}
		c.JSON(http.StatusBadRequest, renderResponse{
			Status:    StatusError,
			Error:     "invalid JSON payload: " + err.Error(),
			ErrorCode: code,
		})
		return req, false
	}
	// A single document is the contract. Decode once more and require EOF:
	// without this, `{...}{...}` or `{...}garbage` silently used the first
	// document and ignored the trailing bytes (CR-09).
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		c.JSON(http.StatusBadRequest, renderResponse{
			Status:    StatusError,
			Error:     "invalid JSON payload: unexpected trailing data",
			ErrorCode: ErrCodeInvalidPayload,
		})
		return req, false
	}
	return req, true
}

// Handler is the thin HTTP transport for POST /api/clips/render. It
// owns request binding, validation, and Master enqueue; all business
// logic lives in the cliprender capability (and, in the follow-up
// steps, its worker).
type Handler struct {
	jobsSvc        job.Service
	renderCache    RenderCache
	inputRevisions InputDigestResolver
	log            *zap.Logger

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

// WithRenderCache attaches the deterministic render cache for the
// synchronous hit path (fingerprint → certified locator). When wired,
// an identical POST returns in milliseconds without enqueuing a GPU job.
func (h *Handler) WithRenderCache(c RenderCache) *Handler {
	if h != nil {
		h.renderCache = c
	}
	return h
}

// WithInputDigestResolver attaches the CRA-03 cache guard. When wired, a cache
// record is served only while the resolved input revisions still match the
// digest recorded at publish time; a record without a digest is a miss.
func (h *Handler) WithInputDigestResolver(r InputDigestResolver) *Handler {
	if h != nil {
		h.inputRevisions = r
	}
	return h
}

// RegisterRoutes mounts the clip.render surface under the /clips
// router group (production path /api/clips/render).
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	idem := h.Idempotency
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}
	r.POST("/render", idem, h.Render)
	r.POST("/render/batch", idem, h.RenderBatch)
	r.GET("/render/batch/:batch_id", h.GetRenderBatch)
	r.DELETE("/render/batch/:batch_id", h.CancelRenderBatch)
	r.POST("/render/sequence", idem, h.RenderSequence)
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
	// The body is bounded BEFORE decoding, so an unbounded/streamed JSON
	// document fails at the transport instead of pinning a Master worker.
	req, ok := decodeStrictBody[RenderRequest](c, MaxClipRenderRequestBytes, "request")
	if !ok {
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

	// One identity for cache lookup AND enqueue idempotency. Deriving the
	// correlation from the fingerprint (rather than from a per-request header)
	// makes a retried identical POST return the SAME job, and keeps two
	// different bodies from collapsing onto one queue key (CR-02).
	fp, fpErr := req.Fingerprint()
	if fpErr != nil {
		c.JSON(http.StatusBadRequest, renderResponse{
			Status:    StatusError,
			Error:     "cannot fingerprint request: " + fpErr.Error(),
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}

	// Deterministic cache fast path: identical semantics → same bytes
	// without touching the GPU. Served synchronously in milliseconds.
	if h.renderCache != nil {
		rec, cacheErr := h.renderCache.Get(c.Request.Context(), fp)
		logCacheLookupFailure(h.log, "handler", fp, cacheErr)
		if cacheErr == nil && rec != nil && !cacheInputMatches(c.Request.Context(), h.inputRevisions, &req, rec) {
			h.log.Info("clip.render cache entry stale: input revision changed",
				zap.String("fingerprint", digestPrefix(fp)),
				zap.String("asset_id", rec.AssetID))
			rec = nil
		}
		if cacheErr == nil && rec != nil {
			h.log.Info("clip.render cache hit (handler)",
				zap.String("fingerprint", digestPrefix(fp)),
				zap.String("asset_id", rec.AssetID),
			)
			cachedResponse := gin.H{
				"status":           "CACHED",
				"fingerprint":      fp,
				"asset_id":         rec.AssetID,
				"delivery_status":  rec.DeliveryStatus,
				"delivery_pending": strings.EqualFold(rec.DeliveryStatus, "pending"),
				"drive_file_id":    rec.DriveFileID,
			}
			if strings.EqualFold(rec.DeliveryStatus, "not_required") {
				cachedResponse["content_url"] = AssetContentURL(rec.AssetID)
			}
			cachedResponse["artifact"] = gin.H{
				"storage_key":  rec.StorageKey,
				"artifact_url": rec.ArtifactURL,
				"sha256":       rec.SHA256,
				"size_bytes":   rec.SizeBytes,
			}
			c.JSON(http.StatusOK, cachedResponse)
			return
		}
	}

	enqueued, err := h.jobsSvc.Enqueue(c.Request.Context(), &job.EnqueueRequest{
		Type:          TypeClipRender,
		CorrelationID: "cliprender:" + fp,
		Payload:       &req,
		Scheduling:    &job.SchedulingDescriptor{JobType: TypeClipRender, ResourceClass: SchedulingResourceClass, ExecutionLane: ExecutionLaneSubmit},
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
		Status: enqueuedStatus(enqueued),
	})
}
