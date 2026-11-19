package cliprender

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
func (s *BatchSequence) request(jobIDs, assetIDs []string, cacheHit []bool, items int) (*SequenceRequest, error) {
	byPosition, err := s.transitionsByPosition(items)
	if err != nil {
		return nil, err
	}
	request := &SequenceRequest{RenderPhase: RenderPhaseSequence, Output: s.Output, Clips: make([]SequenceClip, items)}
	for i := 0; i < items; i++ {
		clip := SequenceClip{}
		if cacheHit[i] {
			clip.AssetID = assetIDs[i]
		} else {
			clip.JobID = jobIDs[i]
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
	req, ok := decodeStrictBody[BatchRenderRequest](c, MaxClipRenderBatchBytes, "batch")
	if !ok {
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

	// The declared joins are validated FIRST: a batch whose sequence cannot be
	// rendered must not enqueue its items and then reject the request, leaving
	// N renders running that the caller never sees assembled.
	if req.Sequence != nil {
		if err := req.Sequence.validate(len(req.Items)); err != nil {
			c.JSON(http.StatusBadRequest, renderResponse{
				Status:    StatusError,
				Error:     err.Error(),
				ErrorCode: ErrCodeInvalidPayload,
			})
			return
		}
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
	perPosErr := make([]string, len(req.Items))

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
			rec, cacheErr := h.renderCache.Get(c.Request.Context(), fp)
			logCacheLookupFailure(h.log, "handler_batch", fp, cacheErr)
			if cacheErr == nil && rec != nil {
				perPosCacheHit[i] = true
				perPosAssetID[i] = rec.AssetID
				// A cache hit is NOT a job: the item's identity is the asset it
				// reuses. The historical "cache:<fp>" job_id answered 404 when a
				// caller polled it, so the field stays empty.
				h.log.Info("clip.render batch cache hit",
					zap.String("batch_id", batchID),
					zap.Int("position", i),
					zap.String("fingerprint", digestPrefix(fp)),
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
	enqueueFailed := 0
	for _, fp := range uniqueFPs {
		pos := firstPos[fp]
		if perPosCacheHit[pos] {
			continue
		}
		item := req.Items[pos]
		// Fingerprint is the canonical dedup key; also scoping the
		// correlation to the batch keeps the global correlation space
		// collision-free without relying on randomness.
		perItemCorrelation := batchID + ":" + digestPrefix(fp)
		enqueued, err := h.jobsSvc.Enqueue(c.Request.Context(), &job.EnqueueRequest{
			Type:          TypeClipRender,
			CorrelationID: perItemCorrelation,
			Payload:       &item,
			Scheduling:    &job.SchedulingDescriptor{JobType: TypeClipRender, ResourceClass: SchedulingResourceClass, ExecutionLane: ExecutionLaneSubmit},
		})
		if err != nil {
			h.log.Error("clip.render batch enqueue failed",
				zap.String("batch_id", batchID),
				zap.Int("position", pos),
				zap.String("fingerprint", digestPrefix(fp)),
				zap.Error(err))
			// Keep fanning out: returning here would abandon the jobs already
			// created without telling the caller their ids. The response below
			// reports every position with its own outcome.
			enqueueFailed++
			perPosErr[pos] = fmt.Sprintf("item %d enqueue: %v", pos, err)
			continue
		}
		perPosJobID[pos] = enqueued.ID
		h.log.Info("clip.render batch enqueued",
			zap.String("batch_id", batchID),
			zap.Int("position", pos),
			zap.String("job_id", enqueued.ID),
			zap.String("fingerprint", digestPrefix(fp)),
		)
	}

	// The per-item outcome is built BEFORE any further enqueue, so a failed
	// fan-out can still report the ids it created.
	jobs := make([]batchItemResponse, len(req.Items))
	accepted := 0
	for _, fp := range uniqueFPs {
		if perPosErr[firstPos[fp]] == "" {
			accepted++
		}
	}
	for i, fp := range perItemFP {
		canonical := firstPos[fp]
		item := batchItemResponse{
			Position:    i,
			Fingerprint: fp,
			JobID:       perPosJobID[canonical],
			CacheHit:    perPosCacheHit[canonical],
			AssetID:     perPosAssetID[canonical],
		}
		switch {
		case perPosErr[canonical] != "":
			item.Status = StatusError
			item.Error = perPosErr[canonical]
		case perPosCacheHit[canonical]:
			item.Status = "CACHED"
		default:
			item.Status = StatusQueued
		}
		jobs[i] = item
	}
	if enqueueFailed > 0 {
		h.log.Error("clip.render batch partially enqueued",
			zap.String("batch_id", batchID),
			zap.Int("unique", len(uniqueFPs)),
			zap.Int("failed", enqueueFailed),
			zap.Int("accepted", accepted))
		// The items that did enqueue are real renders the caller must be able
		// to find (and cancel). The assembly is deliberately NOT enqueued: it
		// would wait forever on the positions that never started.
		c.JSON(http.StatusInternalServerError, batchRenderResponse{
			BatchID:  batchID,
			Accepted: accepted,
			Jobs:     jobs,
			Error: fmt.Sprintf("%d of %d unique items failed to enqueue; the jobs listed above WERE created and must be cancelled if the batch is abandoned",
				enqueueFailed, len(uniqueFPs)),
		})
		return
	}

	// Sequence assembly, in the SAME request. The job is enqueued after the
	// items so it always finds their rows; it waits on any render still in
	// flight (job.DeferredAfter, which spends no retry budget) and then joins
	// what they published. Cache hits have no job to wait on, so they travel as
	// asset ids instead.
	sequenceJobID := ""
	if req.Sequence != nil {
		sequenceRequest, seqErr := req.Sequence.request(perPosJobID, perPosAssetID, perPosCacheHit, len(req.Items))
		if seqErr != nil {
			c.JSON(http.StatusBadRequest, renderResponse{
				Status:    StatusError,
				Error:     seqErr.Error(),
				ErrorCode: ErrCodeInvalidPayload,
			})
			return
		}
		enqueued, seqErr := h.jobsSvc.Enqueue(c.Request.Context(), &job.EnqueueRequest{
			Type:          TypeClipRender,
			CorrelationID: batchID + ":sequence",
			Payload:       sequenceRequest,
			Scheduling:    &job.SchedulingDescriptor{JobType: TypeClipRender, ResourceClass: SchedulingResourceClass, ExecutionLane: ExecutionLaneSubmit},
		})
		if seqErr != nil {
			h.log.Error("clip.render batch sequence enqueue failed",
				zap.String("batch_id", batchID),
				zap.Error(seqErr))
			// The item renders were created; return them with the assembly error
			// instead of a bare 500 that loses their ids.
			c.JSON(http.StatusInternalServerError, batchRenderResponse{
				BatchID:  batchID,
				Accepted: accepted,
				Jobs:     jobs,
				Error:    fmt.Sprintf("sequence enqueue failed: %v; the %d listed item jobs WERE created", seqErr, accepted),
			})
			return
		}
		sequenceJobID = enqueued.ID
		h.log.Info("clip.render batch sequence enqueued",
			zap.String("batch_id", batchID),
			zap.String("job_id", sequenceJobID),
			zap.Int("clips", len(sequenceRequest.Clips)),
			zap.Int("transitions", len(req.Sequence.Transitions)))
	}

	h.log.Info("clip.render batch accepted",
		zap.String("batch_id", batchID),
		zap.Int("items", len(req.Items)),
		zap.Int("unique", len(uniqueFPs)),
		zap.Int("accepted", accepted),
	)

	c.JSON(http.StatusAccepted, batchRenderResponse{
		BatchID:       batchID,
		Accepted:      accepted,
		Jobs:          jobs,
		SequenceJobID: sequenceJobID,
	})
}
