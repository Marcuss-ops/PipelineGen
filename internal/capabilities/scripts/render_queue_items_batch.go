package scriptgeneration

// render_queue_items_batch.go owns the dedicated per-item render path that
// survives the manifest-timeline cutover: maps and chain items keep their own
// render pipelines (dynamic-map admission and asset lifecycle; chain segment
// rebasing) and their own certified artifacts. Ordinary overlay items are NOT
// rendered here — they compose the single manifest timeline artifact
// (overlay_manifest_timeline.go).

import (
	"context"
	"fmt"
	"sort"
	"sync"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// enqueueDedicatedOverlayItems renders and publishes each map/chain item as an
// independent certified artifact through its dedicated pipeline. The returned
// reference carries one item row per artifact in plan order; the manifest
// timeline caller merges these rows into its own reference.
//
// The items are submitted through bounded pools (the map class at its own
// width — one LOD raster pyramid per map — the rest at the configured width),
// so the GPU lane and the per-item pre/post chain overlap exactly as they did
// before the cutover. Failure is FAIL-FAST: the pool cancels the remaining
// items and the error names the item it came from.
func (e *QueueRenderEnqueuer) enqueueDedicatedOverlayItems(ctx context.Context, plan capoverlay.OverlayPlan, candidates []overlayItemCandidate) (RenderReference, error) {
	if len(candidates) == 0 {
		return RenderReference{}, nil
	}
	// OverlayTimelineAdmission before any expensive render: dedicated items
	// keep their per-item replace semantics in the final handoff, so their
	// frame windows must not share even one frame. Fail early instead of
	// rendering artifacts the final job will reject. The final publication
	// gate remains as defense-in-depth.
	admissionItems := make([]capoverlay.OverlayItem, 0, len(candidates))
	for _, candidate := range candidates {
		start, end := overlayItemWindow(candidate.source)
		admissionItems = append(admissionItems, capoverlay.OverlayItem{
			ID: candidate.source.ID, SceneID: candidate.source.SceneID,
			StartUS: start, DurationUS: end - start,
		})
	}
	if err := capoverlay.AdmitOverlayTimeline(admissionItems, plan.FPSNum, plan.FPSDen); err != nil {
		return RenderReference{}, err
	}
	var mapCandidates, otherCandidates []overlayItemCandidate
	for _, candidate := range candidates {
		if candidate.source.Map != nil {
			mapCandidates = append(mapCandidates, candidate)
		} else {
			otherCandidates = append(otherCandidates, candidate)
		}
	}
	renderOne := func(opCtx context.Context, candidate overlayItemCandidate) (overlayItemRenderResult, error) {
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
			// pre-materialized OUTSIDE the submission path. The enqueue below
			// consumes the durable cache entry through the background fold, so
			// its boundary stays thin and the map prepare is attributable to
			// this phase. A prepare already running for the same map is JOINED,
			// never doubled.
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
	workers := resolveOverlayItemWorkers(e.itemRenderWorkers())
	observability.OverlayItemRenderPoolSize.Set(float64(workers))
	// The map class has its own width (overlayMapRenderWidth, one LOD raster
	// pyramid per map). Every map video starts rendering HERE, ahead of its
	// item's submission: the map pool below joins each prepare and then
	// submits through a thin enqueue. Failures are not fatal here: the item's
	// own prepare retries and attributes the error to the item.
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
					return "", e.PreMaterializeDynamicMapVideo(opCtx, child)
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
			part, err := concurrent.Map(ctx, otherCandidates, workers,
				func(opCtx context.Context, _ int, candidate overlayItemCandidate) (overlayItemRenderResult, error) {
					return renderOne(opCtx, candidate)
				})
			if err != nil {
				errCh <- err
				return
			}
			mu.Lock()
			results = append(results, part...)
			mu.Unlock()
		}()
	}
	if len(mapCandidates) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			part, err := concurrent.Map(ctx, mapCandidates, overlayMapRenderWidth,
				func(opCtx context.Context, _ int, candidate overlayItemCandidate) (overlayItemRenderResult, error) {
					return renderOne(opCtx, candidate)
				})
			if err != nil {
				errCh <- err
				return
			}
			mu.Lock()
			results = append(results, part...)
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
	for _, result := range results {
		items = append(items, result.item)
		items = append(items, result.additionalItems...)
	}
	order := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		order[candidate.source.ID] = candidate.index
	}
	sortRenderItemsByPlanOrder(items, order)
	first := results[0].full
	first.Items = items
	return first, nil
}

// sortRenderItemsByPlanOrder restores the parent plan's editorial order (the
// child plan identity depends on the original plan index).
func sortRenderItemsByPlanOrder(items []OverlayItemRenderReference, order map[string]int) {
	sort.SliceStable(items, func(i, j int) bool {
		return order[items[i].ItemID] < order[items[j].ItemID]
	})
}
