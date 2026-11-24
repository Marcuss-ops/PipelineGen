package cliprender

import (
	"context"
	"fmt"
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// batchFanout is the per-position state of one batch. It exists so the cache,
// sequence and fan-out steps can share the same arrays instead of passing nine
// slices around: a dedup mistake in that plumbing was exactly what collapsed
// distinct items onto one job.
type batchFanout struct {
	batchID              string
	items                []RenderRequest
	perItemFP            []string
	uniqueFPs            []string
	firstPos             map[string]int
	perPosJobID          []string
	perPosStatus         []string
	perPosCacheHit       []bool
	perPosAssetID        []string
	perPosDeliveryStatus []string
	perPosDriveFileID    []string
	perPosErr            []string
	perPosCanonical      []int
}

func newBatchFanout(items []RenderRequest, perItemFP []string) *batchFanout {
	f := &batchFanout{
		batchID:              BatchFingerprint(perItemFP),
		items:                items,
		perItemFP:            perItemFP,
		firstPos:             make(map[string]int, len(items)),
		uniqueFPs:            make([]string, 0, len(items)),
		perPosJobID:          make([]string, len(items)),
		perPosStatus:         make([]string, len(items)),
		perPosCacheHit:       make([]bool, len(items)),
		perPosAssetID:        make([]string, len(items)),
		perPosDeliveryStatus: make([]string, len(items)),
		perPosDriveFileID:    make([]string, len(items)),
		perPosErr:            make([]string, len(items)),
		perPosCanonical:      make([]int, len(items)),
	}
	// Fingerprint → canonical position (first occurrence). Duplicates in the
	// same batch reuse the first job without a second enqueue.
	for i, fp := range perItemFP {
		if _, seen := f.firstPos[fp]; seen {
			continue
		}
		f.firstPos[fp] = i
		f.uniqueFPs = append(f.uniqueFPs, fp)
	}
	for i, fp := range perItemFP {
		f.perPosCanonical[i] = f.firstPos[fp]
	}
	return f
}

// applyBatchCacheHits resolves all unique fingerprints with ONE cache
// round-trip when the cache supports it; the sequential Get fallback keeps every
// existing implementation compatible (CR-05). A hit is served without touching
// the GPU and is NOT a job: the item's identity is the asset it reuses.
func (h *Handler) applyBatchCacheHits(ctx context.Context, f *batchFanout) {
	cacheRecords := make(map[string]*RenderCacheRecord, len(f.uniqueFPs))
	cacheBatchOK := false
	if h.renderCache != nil && len(f.uniqueFPs) > 0 {
		if batch, ok := h.renderCache.(RenderCacheBatch); ok {
			records, cacheErr := batch.GetMany(ctx, f.uniqueFPs)
			if cacheErr != nil {
				logCacheLookupFailure(h.log, "handler_batch", "", cacheErr)
			} else {
				cacheRecords = records
				cacheBatchOK = true
			}
		}
	}
	for _, fp := range f.uniqueFPs {
		if h.renderCache == nil {
			continue
		}
		pos := f.firstPos[fp]
		rec := cacheRecords[fp]
		if !cacheBatchOK {
			var cacheErr error
			rec, cacheErr = h.renderCache.Get(ctx, fp)
			logCacheLookupFailure(h.log, "handler_batch", fp, cacheErr)
			if cacheErr != nil {
				rec = nil
			}
		}
		if rec != nil {
			f.perPosCacheHit[pos] = true
			f.perPosAssetID[pos] = rec.AssetID
			f.perPosDeliveryStatus[pos] = rec.DeliveryStatus
			f.perPosDriveFileID[pos] = rec.DriveFileID
			// The historical "cache:<fp>" job_id answered 404 when a caller
			// polled it, so the field stays empty.
			h.log.Info("clip.render batch cache hit",
				zap.String("batch_id", f.batchID),
				zap.Int("position", pos),
				zap.String("fingerprint", digestPrefix(fp)),
				zap.String("asset_id", rec.AssetID),
			)
		}
	}
}

// recoveryItems lists one cold item per canonical position, which is what the
// sequence job needs to re-resolve anything the fan-out fails to enqueue.
func (f *batchFanout) recoveryItems() []BatchRecoveryItem {
	recoveryItems := make([]BatchRecoveryItem, 0, len(f.uniqueFPs))
	seen := make(map[int]bool, len(f.uniqueFPs))
	for i := range f.items {
		if f.perPosCacheHit[i] {
			continue
		}
		position := f.perPosCanonical[i]
		if seen[position] {
			continue
		}
		seen[position] = true
		recoveryItems = append(recoveryItems, BatchRecoveryItem{Position: position, Request: f.items[position]})
	}
	return recoveryItems
}

// batchSequenceFailure separates a caller-fixable 400 (invalid payload) from a
// 500 that still carries the batch envelope (enqueue failed, nothing created).
type batchSequenceFailure struct {
	invalid bool
	message string
}

func (h *Handler) enqueueBatchSequence(ctx context.Context, f *batchFanout, seq *BatchSequence, items int) (string, *batchSequenceFailure) {
	recoveryItems := f.recoveryItems()
	var recovery *SequenceBatchRecovery
	if len(recoveryItems) > 0 {
		recovery = &SequenceBatchRecovery{BatchID: f.batchID, Items: recoveryItems}
	}
	sequenceRequest, err := seq.request(f.perPosAssetID, f.perPosCacheHit, f.perPosCanonical, recovery, items)
	if err != nil {
		return "", &batchSequenceFailure{invalid: true, message: err.Error()}
	}
	sequenceFP, err := sequenceRequest.Fingerprint()
	if err != nil {
		return "", &batchSequenceFailure{invalid: true, message: err.Error()}
	}
	enqueued, err := h.jobsSvc.Enqueue(ctx, &job.EnqueueRequest{
		Type:          TypeClipRender,
		CorrelationID: f.batchID + ":sequence:" + sequenceFP,
		Payload:       sequenceRequest,
		Scheduling:    &job.SchedulingDescriptor{JobType: TypeClipRender, ResourceClass: SchedulingResourceClass, ExecutionLane: ExecutionLaneSubmit},
	})
	if err != nil {
		h.log.Error("clip.render batch sequence enqueue failed",
			zap.String("batch_id", f.batchID),
			zap.Error(err))
		return "", &batchSequenceFailure{message: fmt.Sprintf("sequence enqueue failed: %v; no item jobs were created", err)}
	}
	h.log.Info("clip.render batch sequence enqueued",
		zap.String("batch_id", f.batchID),
		zap.String("job_id", enqueued.ID),
		zap.Int("clips", len(sequenceRequest.Clips)),
		zap.Int("recovery_items", len(recoveryItems)),
		zap.Int("transitions", len(seq.Transitions)))
	return enqueued.ID, nil
}

// enqueueBatchItems enqueues each unique fingerprint that missed the cache.
// Each item must carry a distinct CorrelationID: the request-level X-Request-ID
// (corid in context) is identical for every item in the batch and the queue
// deduplicates on (type, correlation_id). Without a per-item suffix, N distinct
// payloads collapse to one job — the exact bug observed at 06:27:20/40 where 3
// distinct clips reused job_…_e7043302. Deriving the suffix from the content
// fingerprint keeps retries idempotent while distinct clips never collide.
func (h *Handler) enqueueBatchItems(ctx context.Context, f *batchFanout) int {
	enqueueFailed := 0
	for _, fp := range f.uniqueFPs {
		pos := f.firstPos[fp]
		if f.perPosCacheHit[pos] {
			continue
		}
		item := f.items[pos]
		// Fingerprint is the canonical dedup key; also scoping the correlation
		// to the batch keeps the global correlation space collision-free
		// without relying on randomness.
		perItemCorrelation := f.batchID + ":" + digestPrefix(fp)
		enqueued, err := h.jobsSvc.Enqueue(ctx, &job.EnqueueRequest{
			Type:          TypeClipRender,
			CorrelationID: perItemCorrelation,
			Payload:       &item,
			Scheduling:    &job.SchedulingDescriptor{JobType: TypeClipRender, ResourceClass: SchedulingResourceClass, ExecutionLane: ExecutionLaneSubmit},
		})
		if err != nil {
			h.log.Error("clip.render batch enqueue failed",
				zap.String("batch_id", f.batchID),
				zap.Int("position", pos),
				zap.String("fingerprint", digestPrefix(fp)),
				zap.Error(err))
			// Keep fanning out: returning here would abandon the jobs already
			// created without telling the caller their ids. The response
			// reports every position with its own outcome.
			enqueueFailed++
			f.perPosErr[pos] = fmt.Sprintf("item %d enqueue: %v", pos, err)
			continue
		}
		f.perPosJobID[pos] = enqueued.ID
		f.perPosStatus[pos] = enqueuedStatus(enqueued)
		h.log.Info("clip.render batch enqueued",
			zap.String("batch_id", f.batchID),
			zap.Int("position", pos),
			zap.String("job_id", enqueued.ID),
			zap.String("fingerprint", digestPrefix(fp)),
		)
	}
	return enqueueFailed
}

// buildJobsResponse projects the per-position state onto the 202 envelope.
// accepted counts POSITIONS (cache hit + queued), not unique fingerprints:
// three identical items all have a valid job and the client must see
// accepted=3.
func (f *batchFanout) buildJobsResponse() ([]batchItemResponse, int) {
	accepted := 0
	for _, fp := range f.perItemFP {
		if f.perPosErr[f.firstPos[fp]] == "" {
			accepted++
		}
	}
	jobs := make([]batchItemResponse, len(f.items))
	for i, fp := range f.perItemFP {
		canonical := f.firstPos[fp]
		item := batchItemResponse{
			Position:        i,
			Fingerprint:     fp,
			JobID:           f.perPosJobID[canonical],
			CacheHit:        f.perPosCacheHit[canonical],
			AssetID:         f.perPosAssetID[canonical],
			DeliveryStatus:  f.perPosDeliveryStatus[canonical],
			DeliveryPending: f.perPosDeliveryStatus[canonical] == "pending",
			DriveFileID:     f.perPosDriveFileID[canonical],
		}
		switch {
		case f.perPosErr[canonical] != "":
			item.Status = StatusError
			item.Error = f.perPosErr[canonical]
		case f.perPosCacheHit[canonical]:
			item.Status = "CACHED"
			if strings.EqualFold(f.perPosDeliveryStatus[canonical], "not_required") {
				item.ContentURL = AssetContentURL(f.perPosAssetID[canonical])
			}
		case f.perPosStatus[canonical] != "":
			// An idempotent replay can return an existing job that is not
			// QUEUED (RUNNING/FAILED); report the durable status (CR-02).
			item.Status = f.perPosStatus[canonical]
		default:
			item.Status = StatusQueued
		}
		jobs[i] = item
	}
	return jobs, accepted
}
