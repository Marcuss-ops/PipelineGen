package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"sync"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// enqueueSeparateOverlayItems renders and publishes each semantic overlay as
// an independent short video. A composite entity image remains one semantic
// item and therefore produces one artifact with multiple image layers. The
// returned reference is the first artifact
// for backwards-compatible callers; every artifact is published by the same
// application-owned publisher and receives its own receipt.
//
// The items are submitted through a BOUNDED POOL, not one at a time. The
// previous body submitted a child plan and then blocked on that job's terminal
// state before it would even submit the next one, so exactly one render was
// ever in flight: the GPU lane and the per-item pre/post chain (asset
// materialize, upload, ffprobe contract, Drive publish) could never overlap.
// That is visible in the measurement it produced — on the 5+5 run the overlay
// stage reported 22.7 s of render work but cost 44.0 s of wall, and the worker's
// own journal shows the 10 completions spaced a constant 4-5 s apart for 40 s.
//
// Ordering, identity and error attribution are preserved: results come back in
// plan order, each child keeps its original plan index, and a failure still
// names the item it came from. Failure is FAIL-FAST — the pool cancels the
// remaining items, exactly as the sequential loop stopped at the first error
// instead of submitting the rest.
func (e *QueueRenderEnqueuer) enqueueSeparateOverlayItems(ctx context.Context, plan capoverlay.OverlayPlan) (RenderReference, error) {
	// A background is a canvas primitive, not a user-facing overlay. It is
	// copied into each child plan when the parent uses the first-class
	// Background field; a legacy background item must never become its own
	// Drive video.
	candidates := overlayRenderableCandidates(plan)
	if len(candidates) == 0 {
		return RenderReference{}, nil
	}
	// The bound is published before the work starts, so the pool an operator
	// reads is the one this batch actually ran under (and not a later edit of
	// the config).
	// In-flight width policy (see resolveOverlayItemWorkers): this is
	// pipelining back-pressure, not GPU admission — gpu_lanes stays the sole
	// GPU authority.
	workers := resolveOverlayItemWorkers(e.itemRenderWorkers())
	// Classi di risorse distinte (§10): le mappe complesse restano una classe
	// separata con la propria larghezza (overlayMapRenderWidth, una piramide
	// raster LOD alla volta PER MAPPA), testi/frasi e immagini/entità restano in
	// pipeline alla larghezza configurata. Non serializzare più l'intero
	// fan-out a 1 quando c'è una mappa: solo la classe mappe ha la sua width.
	var mapCandidates, otherCandidates []overlayItemCandidate
	for _, candidate := range candidates {
		if candidate.source.Map != nil {
			mapCandidates = append(mapCandidates, candidate)
		} else {
			otherCandidates = append(otherCandidates, candidate)
		}
	}
	renderOne := func(opCtx context.Context, _ int, candidate overlayItemCandidate) (overlayItemRenderResult, error) {
		// Measured concurrency, not assumed: the gauge rises exactly while a
		// render is awaiting its terminal state, so its peak is the pipelining
		// depth actually achieved.
		observability.OverlayItemRenderInFlight.Inc()
		defer observability.OverlayItemRenderInFlight.Dec()
		child, metadata, err := separateOverlayItemPlan(plan, candidate.source, candidate.index)
		if err != nil {
			return overlayItemRenderResult{}, err
		}
		// A final job still requires every Drive artifact before its master is
		// submitted, but publishing each item inline serializes Drive latency
		// with the next GPU render. Let the existing bounded, run-scoped
		// publication pool drain the uploads; the runner joins that pool at the
		// final-job boundary before building the master payload.
		if child.RequireDriveBeforeReturn && e.asyncPublication && e.publisher != nil {
			child.RequireDriveBeforeReturn = false
		}
		if candidate.source.Map != nil {
			// PREPARE (TICKET-OVERLAY-THROUGHPUT (c)): the dynamic map video is
			// pre-materialized OUTSIDE the submission path — «le mappe vanno
			// pre-materializzate fuori dal percorso di sottomissione (stessa
			// direzione di P3, ma sul prepare)». The enqueue below consumes the
			// durable cache entry through the background fold, so its boundary
			// stays thin and the map prepare is attributable to this phase. A
			// prepare already running for the same map is JOINED, never doubled.
			if err := e.PreMaterializeDynamicMapVideo(opCtx, child); err != nil {
				return overlayItemRenderResult{}, fmt.Errorf("prepare overlay map item %q: %w", candidate.source.ID, err)
			}
		}
		ref, err := e.enqueueChrononPlan(opCtx, child, metadata)
		if err != nil {
			return overlayItemRenderResult{}, fmt.Errorf("render overlay item %q: %w", candidate.source.ID, err)
		}
		return overlayItemRenderResult{
			full:  ref,
			order: candidate.index,
			item: OverlayItemRenderReference{
				ItemID: candidate.source.ID, JobID: ref.JobID, Status: ref.Status, Artifact: ref.Artifact,
			},
		}, nil
	}
	renderBatch := func(opCtx context.Context, work overlayBatchWork) (overlayItemRenderResult, error) {
		observability.OverlayItemRenderInFlight.Inc()
		defer observability.OverlayItemRenderInFlight.Dec()
		child, err := fusedOverlayBatchPlan(plan, work)
		if err != nil {
			return overlayItemRenderResult{}, err
		}
		if child.RequireDriveBeforeReturn && e.asyncPublication && e.publisher != nil {
			child.RequireDriveBeforeReturn = false
		}
		batchEndUS := work.startUS + child.DurationMS*1000
		metadata := &overlayItemPublicationMetadata{
			JobID:       textutil.FirstNonEmpty(plan.DriveJobID, plan.PlanID),
			ResultJobID: textutil.FirstNonEmpty(plan.ResultJobID, plan.DriveJobID),
			ItemID:      "batch:" + work.id, BatchID: work.id, ItemKind: "overlay_batch",
			SourceStartUS: work.startUS, SourceEndUS: batchEndUS,
			TargetDurationUS: batchEndUS - work.startUS, BatchEndUS: batchEndUS,
		}
		for _, candidate := range work.candidates {
			metadata.BatchItemIDs = append(metadata.BatchItemIDs, candidate.source.ID)
		}
		ref, err := e.enqueueChrononPlan(opCtx, child, metadata)
		if err != nil {
			return overlayItemRenderResult{}, fmt.Errorf("render overlay batch %s: %w", work.id, err)
		}
		if ref.Artifact == nil {
			return overlayItemRenderResult{}, fmt.Errorf("render overlay batch %s completed without a certified artifact", work.id)
		}
		// A tileable session produced independent item MP4s. They must not
		// be collapsed back into an editorial composite in the final handoff.
		if len(ref.Items) > 0 {
			return overlayItemRenderResult{
				full: ref, item: ref.Items[0], additionalItems: ref.Items[1:],
				order: work.candidates[0].index,
			}, nil
		}
		batch := &OverlayBatchRenderReference{
			BatchID: work.id, SceneID: work.sceneID, StartUS: work.startUS, EndUS: batchEndUS,
			JobID: ref.JobID, Status: ref.Status, Artifact: ref.Artifact,
			ItemIDs: make([]string, 0, len(work.candidates)),
		}
		itemRefs := make([]OverlayItemRenderReference, 0, len(work.candidates))
		for _, candidate := range work.candidates {
			batch.ItemIDs = append(batch.ItemIDs, candidate.source.ID)
			itemRefs = append(itemRefs, OverlayItemRenderReference{
				ItemID: candidate.source.ID, JobID: ref.JobID, Status: ref.Status, BatchID: work.id,
			})
		}
		return overlayItemRenderResult{
			full: ref, item: itemRefs[0], additionalItems: itemRefs[1:], batch: batch,
			order: work.candidates[0].index,
		}, nil
	}
	// OverlayTimelineAdmission before any expensive render: final frame windows
	// after all duration extensions must not share even one frame (master uses
	// mode=replace). Fail early instead of rendering artifacts the final job
	// will reject. The final publication gate remains as defense-in-depth.
	otherWorks := make([]overlayBatchWork, 0, len(otherCandidates))
	if e.overlayWindowBatching {
		otherWorks = planOverlayBatchWork(plan, otherCandidates)
	} else {
		for _, candidate := range otherCandidates {
			start, end := overlayItemWindow(candidate.source)
			otherWorks = append(otherWorks, overlayBatchWork{sceneID: candidate.source.SceneID, startUS: start, endUS: end, candidates: []overlayItemCandidate{candidate}})
		}
	}
	admissionItems := make([]capoverlay.OverlayItem, 0, len(otherWorks)+len(mapCandidates))
	for _, work := range otherWorks {
		admissionItems = append(admissionItems, overlayBatchAdmissionItem(work))
	}
	for _, candidate := range mapCandidates {
		start, end := overlayItemWindow(candidate.source)
		admissionItems = append(admissionItems, capoverlay.OverlayItem{ID: candidate.source.ID, SceneID: candidate.source.SceneID, StartUS: start, DurationUS: end - start})
	}
	if err := capoverlay.AdmitOverlayTimeline(admissionItems, plan.FPSNum, plan.FPSDen); err != nil {
		return RenderReference{}, err
	}
	observability.OverlayItemRenderPoolSize.Set(float64(workers))
	// Two concurrent pools joined at the end: non-map items at the configured
	// width, map items at overlayMapRenderWidth (one LOD raster pyramid per
	// map, two measured-fit pyramids at once — see the constant's note).
	// The old barrier (all others, then maps) is removed; GPU admission
	// (worker gpu_lanes) remains the final authority when both classes contend
	// for a saturated GPU.
	// PREPARE phase for the map class: every map video starts rendering HERE,
	// ahead of its item's submission, at the class width. The map pool below
	// joins each prepare (joinDynamicMapVideoRender) and then submits through a
	// thin enqueue, so the Chronon map render — the submission path's measured
	// congestion (TICKET-OVERLAY-THROUGHPUT A.2) — is paid by the prepare
	// phase. Failures are not fatal here: the item's own prepare retries and
	// attributes the error to the item.
	if len(mapCandidates) > 0 {
		go func() {
			_, _ = concurrent.Map(ctx, mapCandidates, overlayMapRenderWidth,
				func(opCtx context.Context, _ int, candidate overlayItemCandidate) (string, error) {
					child, _, err := separateOverlayItemPlan(plan, candidate.source, candidate.index)
					if err != nil {
						return "", err
					}
					if child.RequireDriveBeforeReturn && e.asyncPublication && e.publisher != nil {
						child.RequireDriveBeforeReturn = false
					}
					err = e.PreMaterializeDynamicMapVideo(opCtx, child)
					return "", err
				})
		}()
	}
	var results []overlayItemRenderResult
	var mu sync.Mutex
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	if len(otherCandidates) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			otherResults, err := concurrent.Map(ctx, otherWorks, workers, func(opCtx context.Context, _ int, work overlayBatchWork) (overlayItemRenderResult, error) {
				if len(work.candidates) == 1 {
					return renderOne(opCtx, 0, work.candidates[0])
				}
				return renderBatch(opCtx, work)
			})
			if err != nil {
				errCh <- err
				return
			}
			mu.Lock()
			results = append(results, otherResults...)
			mu.Unlock()
		}()
	}
	if len(mapCandidates) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mapResults, err := concurrent.Map(ctx, mapCandidates, overlayMapRenderWidth, renderOne)
			if err != nil {
				errCh <- err
				return
			}
			mu.Lock()
			results = append(results, mapResults...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return RenderReference{}, err
		}
	}
	// Ripristina l'ordine di piano (posizione originale), non alfabetico:
	// l'identità child (<plan>:item:%03d:<id>) dipende dalla posizione.
	sort.Slice(results, func(i, j int) bool { return results[i].order < results[j].order })
	items := make([]OverlayItemRenderReference, 0, len(results))
	batches := make([]OverlayBatchRenderReference, 0)
	for _, result := range results {
		items = append(items, result.item)
		items = append(items, result.additionalItems...)
		if result.batch != nil {
			batches = append(batches, *result.batch)
		}
	}
	order := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		order[candidate.source.ID] = candidate.index
	}
	sort.Slice(items, func(i, j int) bool { return order[items[i].ItemID] < order[items[j].ItemID] })
	first := results[0].full
	first.Items = items
	first.Batches = batches
	return first, nil
}
