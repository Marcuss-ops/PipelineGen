package cliprender

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// MaxClipRenderBatchItems caps POST /api/clips/render/batch fan-out.
// 50 is the certification leg (50 clips sotto carico) and bounds how many
// Master jobs one HTTP request can create.
const MaxClipRenderBatchItems = 50

const MaxClipRenderBatchBytes int64 = 5 << 20 // 5 MiB

// BatchRenderRequest is the batch envelope. Each item is a full
// RenderRequest; Destination drive folder may be overridden per-item.
type BatchRenderRequest struct {
	Items []RenderRequest `json:"items"`
}

// batchItemResponse is the per-item entry in the 202 batch response.
type batchItemResponse struct {
	Position    int    `json:"position"`
	Fingerprint string `json:"fingerprint,omitempty"`
	JobID       string `json:"job_id,omitempty"`
	Status      string `json:"status"`
	CacheHit    bool   `json:"cache_hit,omitempty"`
	AssetID     string `json:"asset_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

// batchRenderResponse is the 202 envelope for POST /api/clips/render/batch.
type batchRenderResponse struct {
	BatchID  string              `json:"batch_id"`
	Accepted int                 `json:"accepted"`
	Jobs     []batchItemResponse `json:"jobs"`
}

// RenderBatch handles POST /api/clips/render/batch.
//
// It is a thin transport that reuses the single-item validation (Normalize +
// Validate) and fingerprint (canonical SHA-256 over normalized request) for
// every item. Identical items collapse to one GPU slot: the fingerprint dedup
// map ensures N identical RenderRequests enqueue ONE job and the duplicates
// reuse its job_id. A deterministic cache hit (fingerprint → certified locator)
// is served synchronously in milliseconds without enqueuing.
func (h *Handler) RenderBatch(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxClipRenderBatchBytes)

	var req BatchRenderRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, renderResponse{
				Status:    StatusError,
				Error:     fmt.Sprintf("batch body exceeds the %d byte limit", MaxClipRenderBatchBytes),
				ErrorCode: ErrCodePayloadTooLarge,
			})
			return
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
		return
	}
	if len(req.Items) == 0 {
		c.JSON(http.StatusBadRequest, renderResponse{
			Status:    StatusError,
			Error:     "items is required and must contain at least one clip",
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}
	if len(req.Items) > MaxClipRenderBatchItems {
		c.JSON(http.StatusBadRequest, renderResponse{
			Status:    StatusError,
			Error:     fmt.Sprintf("batch too large: %d items (max %d)", len(req.Items), MaxClipRenderBatchItems),
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

	// Normalize + validate each item, fail the whole batch on first bad item
	// (godlike/07 fail-fast; caller fixes the batch atomically).
	perItemFP := make([]string, len(req.Items))
	for i := range req.Items {
		req.Items[i].Normalize()
		if err := req.Items[i].Validate(); err != nil {
			c.JSON(http.StatusBadRequest, renderResponse{
				Status:    StatusError,
				Error:     fmt.Sprintf("item %d: %v", i, err),
				ErrorCode: ErrCodeInvalidPayload,
			})
			return
		}
		fp, err := req.Items[i].Fingerprint()
		if err != nil {
			c.JSON(http.StatusBadRequest, renderResponse{
				Status:    StatusError,
				Error:     fmt.Sprintf("item %d fingerprint: %v", i, err),
				ErrorCode: ErrCodeInvalidPayload,
			})
			return
		}
		perItemFP[i] = fp
	}

	batchID := BatchFingerprint(perItemFP)

	// Fingerprint → canonical position (first occurrence). Duplicates in the
	// same batch reuse the first job without a second enqueue.
	firstPos := make(map[string]int, len(req.Items))
	perPosJobID := make([]string, len(req.Items))
	perPosCacheHit := make([]bool, len(req.Items))
	perPosAssetID := make([]string, len(req.Items))

	// Try cache first for each unique fingerprint (handler-level fast path).
	// A hit is served without touching the GPU; a miss falls through to enqueue.
	uniqueFPs := make([]string, 0, len(req.Items))
	for i, fp := range perItemFP {
		if _, seen := firstPos[fp]; seen {
			continue
		}
		firstPos[fp] = i
		uniqueFPs = append(uniqueFPs, fp)
		if h.renderCache != nil {
			if rec, err := h.renderCache.Get(c.Request.Context(), fp); err == nil && rec != nil {
				perPosCacheHit[i] = true
				perPosAssetID[i] = rec.AssetID
				// Synthesize a stable job_id from the cached asset so the
				// caller can poll the existing media row instead of a new job.
				perPosJobID[i] = "cache:" + fp[:16]
				h.log.Info("clip.render batch cache hit",
					zap.String("batch_id", batchID),
					zap.Int("position", i),
					zap.String("fingerprint", fp[:16]),
					zap.String("asset_id", rec.AssetID),
				)
			}
		}
	}

	// Enqueue each unique fingerprint that missed the cache.
	// Each item must carry a distinct CorrelationID: the request-level
	// X-Request-ID (corid in context) is identical for every item in the
	// batch and the queue deduplicates on (type, correlation_id). Without
	// a per-item suffix, N distinct payloads collapse to one job — the
	// exact bug observed at 06:27:20/40 where 3 distinct clips reused
	// job_…_e7043302. Derive the suffix from the content fingerprint so
	// retries remain idempotent (same ordered clips → same per-item
	// correlation) while distinct clips never collide.
	for _, fp := range uniqueFPs {
		pos := firstPos[fp]
		if perPosCacheHit[pos] {
			continue
		}
		item := req.Items[pos]
		// Fingerprint is the canonical dedup key; also scoping the
		// correlation to the batch keeps the global correlation space
		// collision-free without relying on randomness.
		perItemCorrelation := batchID + ":" + fp[:16]
		enqueued, err := h.jobsSvc.Enqueue(c.Request.Context(), &job.EnqueueRequest{
			Type:          TypeClipRender,
			CorrelationID: perItemCorrelation,
			Payload:       &item,
		})
		if err != nil {
			h.log.Error("clip.render batch enqueue failed",
				zap.String("batch_id", batchID),
				zap.Int("position", pos),
				zap.String("fingerprint", fp[:16]),
				zap.Error(err))
			apiutil.InternalError(c, fmt.Errorf("item %d enqueue: %w", pos, err))
			return
		}
		perPosJobID[pos] = enqueued.ID
		h.log.Info("clip.render batch enqueued",
			zap.String("batch_id", batchID),
			zap.Int("position", pos),
			zap.String("job_id", enqueued.ID),
			zap.String("fingerprint", fp[:16]),
		)
	}

	jobs := make([]batchItemResponse, len(req.Items))
	for i, fp := range perItemFP {
		canonical := firstPos[fp]
		status := StatusQueued
		if perPosCacheHit[canonical] {
			status = "CACHED"
		}
		jobs[i] = batchItemResponse{
			Position:    i,
			Fingerprint: fp,
			JobID:       perPosJobID[canonical],
			Status:      status,
			CacheHit:    perPosCacheHit[canonical],
			AssetID:     perPosAssetID[canonical],
		}
	}

	// Count accepted as unique enqueues + unique cache hits (deduped).
	accepted := len(uniqueFPs)
	h.log.Info("clip.render batch accepted",
		zap.String("batch_id", batchID),
		zap.Int("items", len(req.Items)),
		zap.Int("unique", len(uniqueFPs)),
		zap.Int("accepted", accepted),
	)

	c.JSON(http.StatusAccepted, batchRenderResponse{
		BatchID:  batchID,
		Accepted: accepted,
		Jobs:     jobs,
	})
}
