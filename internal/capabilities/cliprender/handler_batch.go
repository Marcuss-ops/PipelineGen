package cliprender

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media/clipsequence"
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
	// Sequence asks the batch to assemble the items, in order, into one
	// deliverable. It is optional: without it the batch produces N independent
	// clips, exactly as before. With it the handler submits ONE extra
	// sequence job that waits for the items and joins them, so the caller does
	// not run the assembler itself.
	Sequence *BatchSequence `json:"sequence,omitempty"`
}

// BatchSequence declares the joins of a batch: transitions keyed by the item
// position that CARRIES them (the incoming clip), plus the output settings.
// Positions without a transition stay a hard cut, exactly like the sequence
// contract's rule.
type BatchSequence struct {
	Transitions []BatchSequenceTransition `json:"transitions,omitempty"`
	Output      SequenceOutput            `json:"output,omitempty"`
}

// BatchSequenceTransition is one join of a batch, addressed by position.
type BatchSequenceTransition struct {
	// Position is the 0-based index into items that carries this transition.
	// It must be >= 1: the first clip has no previous clip to join.
	Position   int                        `json:"position"`
	Preset     clipsequence.Preset        `json:"preset"`
	DurationMS int64                      `json:"duration_ms,omitempty"`
	Leak       *clipsequence.LeakOverride `json:"leak,omitempty"`
}

// validate checks the joins on their own. The handler calls it BEFORE it
// enqueues anything, so a bad sequence fails the whole batch with zero jobs
// created instead of leaving N renders running for a request that was rejected.
func (s *BatchSequence) validate(items int) error {
	// The batch embeds the same sequence contract as POST /render/sequence, so
	// it must apply the same filesystem gate: `sequence.output.dir` would
	// otherwise be the one remaining path from an HTTP body to an arbitrary
	// directory write (ticket CR-19-04).
	if strings.TrimSpace(s.Output.Dir) != "" {
		return fmt.Errorf("sequence.output.dir is not accepted over HTTP; the server owns the output location")
	}
	_, err := s.transitionsByPosition(items)
	return err
}

// transitionsByPosition is the single owner of "is this join addressable and
// renderable": a position must exist (>=1, since the first clip has no previous
// clip), appear once, and pass the sequence contract.
func (s *BatchSequence) transitionsByPosition(items int) (map[int]clipsequence.Transition, error) {
	byPosition := make(map[int]clipsequence.Transition, len(s.Transitions))
	for _, transition := range s.Transitions {
		if transition.Position < 1 || transition.Position >= items {
			return nil, fmt.Errorf("sequence.transitions: position %d is outside 1..%d", transition.Position, items-1)
		}
		if _, duplicate := byPosition[transition.Position]; duplicate {
			return nil, fmt.Errorf("sequence.transitions: position %d is declared twice", transition.Position)
		}
		candidate := clipsequence.Transition{Preset: transition.Preset, DurationMS: transition.DurationMS, Leak: transition.Leak}
		if err := clipsequence.ValidateTransition(&candidate); err != nil {
			return nil, fmt.Errorf("sequence.transitions: position %d: %w", transition.Position, err)
		}
		byPosition[transition.Position] = candidate
	}
	return byPosition, nil
}

// request projects the batch declaration onto the canonical sequence payload.
// Cold items travel as batch positions, not job ids: the sequence job is
// enqueued before the items and resolves each position itself (CR-04). Cache
// hits travel as the certified asset they already are.
func (s *BatchSequence) request(assetIDs []string, cacheHit []bool, positions []int, recovery *SequenceBatchRecovery, items int) (*SequenceRequest, error) {
	byPosition, err := s.transitionsByPosition(items)
	if err != nil {
		return nil, err
	}
	if len(positions) != items || len(assetIDs) != items || len(cacheHit) != items {
		return nil, fmt.Errorf("sequence projection: positions/assets/cache arrays must have %d entries", items)
	}
	request := &SequenceRequest{RenderPhase: RenderPhaseSequence, Output: s.Output, Clips: make([]SequenceClip, items), Recovery: recovery}
	for i := 0; i < items; i++ {
		clip := SequenceClip{}
		if cacheHit[i] {
			clip.AssetID = assetIDs[i]
		} else {
			position := positions[i]
			if position < 0 || position >= items {
				return nil, fmt.Errorf("sequence projection: position %d is outside 0..%d", position, items-1)
			}
			clip.BatchPosition = &position
		}
		if transition, ok := byPosition[i]; ok {
			transition := transition
			clip.TransitionIn = &transition
		}
		request.Clips[i] = clip
	}
	return request, nil
}

// batchItemResponse is the per-item entry in the 202 batch response.
type batchItemResponse struct {
	Position        int    `json:"position"`
	Fingerprint     string `json:"fingerprint,omitempty"`
	JobID           string `json:"job_id,omitempty"`
	Status          string `json:"status"`
	CacheHit        bool   `json:"cache_hit,omitempty"`
	AssetID         string `json:"asset_id,omitempty"`
	DeliveryStatus  string `json:"delivery_status,omitempty"`
	DeliveryPending bool   `json:"delivery_pending,omitempty"`
	// ContentURL is the authenticated byte route for object-store delivery
	// (CR-19). Empty on the Drive path, where drive_file_id is the address.
	ContentURL  string `json:"content_url,omitempty"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

// batchRenderResponse is the 202 envelope for POST /api/clips/render/batch.
type batchRenderResponse struct {
	BatchID  string              `json:"batch_id"`
	Accepted int                 `json:"accepted"`
	Unique   int                 `json:"unique"`
	Jobs     []batchItemResponse `json:"jobs"`
	// Error is set on a 500 that still carries the batch envelope: a partial
	// fan-out must never hide the jobs it already created.
	Error string `json:"error,omitempty"`
	// SequenceJobID is the assembly job this batch enqueued, when the request
	// carried a sequence. It is a clip.render job in the sequence phase: it
	// waits for the jobs above and returns the merged artifact paths.
	SequenceJobID string `json:"sequence_job_id,omitempty"`
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
	req, ok := h.decodeBatchRenderRequest(c)
	if !ok {
		return
	}
	perItemFP, ok := normalizeBatchItems(c, req.Items)
	if !ok {
		return
	}

	fanout := newBatchFanout(req.Items, perItemFP)
	h.applyBatchCacheHits(c.Request.Context(), fanout)

	// A sequence batch is made durable BEFORE its items. The sequence job
	// carries the cold item requests and resolves them by the same correlation
	// the fan-out uses, so a process death between the items and the assembly
	// cannot strand the batch (CR-04). The handler still enqueues the items
	// below for latency; both paths dedupe on (type, correlation_id).
	sequenceJobID := ""
	if req.Sequence != nil {
		jobID, failure := h.enqueueBatchSequence(c.Request.Context(), fanout, req.Sequence, len(req.Items))
		if failure != nil {
			if failure.invalid {
				c.JSON(http.StatusBadRequest, renderResponse{
					Status:    StatusError,
					Error:     failure.message,
					ErrorCode: ErrCodeInvalidPayload,
				})
				return
			}
			// No item job was created: failing here leaves the batch atomic
			// and retryable, instead of stranding N renders with no assembly.
			c.JSON(http.StatusInternalServerError, batchRenderResponse{
				BatchID:  fanout.batchID,
				Accepted: 0,
				Unique:   len(fanout.uniqueFPs),
				Error:    failure.message,
			})
			return
		}
		sequenceJobID = jobID
	}

	enqueueFailed := h.enqueueBatchItems(c.Request.Context(), fanout)

	// The per-item outcome is built BEFORE any further enqueue, so a failed
	// fan-out can still report the ids it created.
	jobs, accepted := fanout.buildJobsResponse()
	if enqueueFailed > 0 {
		h.log.Error("clip.render batch partially enqueued",
			zap.String("batch_id", fanout.batchID),
			zap.Int("unique", len(fanout.uniqueFPs)),
			zap.Int("failed", enqueueFailed),
			zap.Int("accepted", accepted))
		// The items that did enqueue are real renders the caller must be able
		// to find (and cancel). The sequence job was already created before the
		// fan-out, so it will recover the positions that failed to enqueue;
		// reporting its id is what makes the partial response actionable.
		message := fmt.Sprintf("%d of %d unique items failed to enqueue; the jobs listed above WERE created",
			enqueueFailed, len(fanout.uniqueFPs))
		if sequenceJobID != "" {
			message += fmt.Sprintf("; sequence job %s WILL recover the missing items", sequenceJobID)
		}
		c.JSON(http.StatusInternalServerError, batchRenderResponse{
			BatchID:       fanout.batchID,
			Accepted:      accepted,
			Unique:        len(fanout.uniqueFPs),
			Jobs:          jobs,
			SequenceJobID: sequenceJobID,
			Error:         message,
		})
		return
	}
	h.log.Info("clip.render batch accepted",
		zap.String("batch_id", fanout.batchID),
		zap.Int("items", len(req.Items)),
		zap.Int("unique", len(fanout.uniqueFPs)),
		zap.Int("accepted", accepted),
	)

	c.JSON(http.StatusAccepted, batchRenderResponse{
		BatchID:       fanout.batchID,
		Accepted:      accepted,
		Unique:        len(fanout.uniqueFPs),
		Jobs:          jobs,
		SequenceJobID: sequenceJobID,
	})
}

// decodeBatchRenderRequest enforces the envelope contract (at least one item,
// fan-out cap, jobs service wiring) and the declared joins BEFORE anything is
// enqueued: a batch whose sequence cannot be rendered must not leave N renders
// running that the caller never sees assembled.
func (h *Handler) decodeBatchRenderRequest(c *gin.Context) (*BatchRenderRequest, bool) {
	req, ok := decodeStrictBody[BatchRenderRequest](c, MaxClipRenderBatchBytes, "batch")
	if !ok {
		return nil, false
	}
	if len(req.Items) == 0 {
		c.JSON(http.StatusBadRequest, renderResponse{
			Status:    StatusError,
			Error:     "items is required and must contain at least one clip",
			ErrorCode: ErrCodeInvalidPayload,
		})
		return nil, false
	}
	if len(req.Items) > MaxClipRenderBatchItems {
		c.JSON(http.StatusBadRequest, renderResponse{
			Status:    StatusError,
			Error:     fmt.Sprintf("batch too large: %d items (max %d)", len(req.Items), MaxClipRenderBatchItems),
			ErrorCode: ErrCodeInvalidPayload,
		})
		return nil, false
	}
	if h.jobsSvc == nil {
		c.JSON(http.StatusServiceUnavailable, renderResponse{
			Status:    StatusError,
			Error:     "jobs service not configured",
			ErrorCode: ErrCodeJobsUnavailable,
		})
		return nil, false
	}
	if req.Sequence != nil {
		if err := req.Sequence.validate(len(req.Items)); err != nil {
			c.JSON(http.StatusBadRequest, renderResponse{
				Status:    StatusError,
				Error:     err.Error(),
				ErrorCode: ErrCodeInvalidPayload,
			})
			return nil, false
		}
	}
	return &req, true
}

// normalizeBatchItems normalizes, validates and fingerprints every item,
// failing the whole batch on the first bad item (godlike/07 fail-fast; the
// caller fixes the batch atomically).
func normalizeBatchItems(c *gin.Context, items []RenderRequest) ([]string, bool) {
	perItemFP := make([]string, len(items))
	for i := range items {
		items[i].Normalize()
		if err := items[i].Validate(); err != nil {
			c.JSON(http.StatusBadRequest, renderResponse{
				Status:    StatusError,
				Error:     fmt.Sprintf("item %d: %v", i, err),
				ErrorCode: ErrCodeInvalidPayload,
			})
			return nil, false
		}
		fp, err := items[i].Fingerprint()
		if err != nil {
			c.JSON(http.StatusBadRequest, renderResponse{
				Status:    StatusError,
				Error:     fmt.Sprintf("item %d fingerprint: %v", i, err),
				ErrorCode: ErrCodeInvalidPayload,
			})
			return nil, false
		}
		perItemFP[i] = fp
	}
	return perItemFP, true
}
