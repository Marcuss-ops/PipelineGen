package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// defaultSeparateItemRenderWorkers is how many per-item overlay renders may be
// in flight at once when the caller does not tune the pool. Tuned 4→2 after
// live queue-wait audit (2026-09-27): with 5-14 overlays, queue wait was 62%
// of wall (7484 ms avg, 18435 max) vs 38% render, driven by 4 concurrent
// GPU jobs queuing behind gpu_lanes=2. Width 2 retains pipelining of the
// per-item pre/post chain while halving queue contention; Depth>4 back-pressure
// below further throttles when the queue is already saturated. The worker's
// gpu_lanes (now 3) remains the sole GPU authority.
const defaultSeparateItemRenderWorkers = 2

// Overlay-batch back-pressure (retuned 2026-09-27). The queue-wait audit showed
// wall time dominated by admission wait (62% of wall at pool width 4 behind
// gpu_lanes=2), so a DEEP batch is clamped below the configured width: an
// operator may temporarily widen the pool, but a large overlay batch must not
// widen the queue behind the worker's fixed gpu_lanes. The rule is a pure
// function of (requested width, batch depth) so it is testable without a queue
// and cannot drift from what the enqueue path actually applies.
const (
	overlayItemBackPressureDepth = 4
	overlayItemBackPressureWidth = 2
	overlayItemDeepDepth         = 8
	overlayItemDeepWidth         = 1
)

// resolveOverlayItemWorkers applies the certified back-pressure rule: a batch
// deeper than overlayItemBackPressureDepth is clamped to
// overlayItemBackPressureWidth, and a batch deeper than overlayItemDeepDepth to
// overlayItemDeepWidth. A requested width already at or below the clamp is left
// alone — the clamp only ever narrows, never widens.
func resolveOverlayItemWorkers(requested, candidates int) int {
	if candidates > overlayItemDeepDepth && requested > overlayItemDeepWidth {
		return overlayItemDeepWidth
	}
	if candidates > overlayItemBackPressureDepth && requested > overlayItemBackPressureWidth {
		return overlayItemBackPressureWidth
	}
	return requested
}

// overlayItemCandidate is one renderable overlay item together with its
// position in the parent plan. The position is preserved because it is part of
// the child plan identity (`<plan>:item:%03d:<id>`), so concurrent work must not
// renumber items.
type overlayItemCandidate struct {
	index  int
	source capoverlay.OverlayItem
}

// overlayItemRenderResult carries both the per-item reference the caller
// publishes in `Items` and the full RenderReference the first item returns for
// backwards compatibility.
type overlayItemRenderResult struct {
	full RenderReference
	item OverlayItemRenderReference
}

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
	candidates := make([]overlayItemCandidate, 0, len(plan.Items))
	for index, source := range plan.Items {
		// A background is a canvas primitive, not a user-facing overlay. It is
		// copied into each child plan when the parent uses the first-class
		// Background field; a legacy background item must never become its own
		// Drive video.
		if strings.EqualFold(strings.TrimSpace(source.TemplateID), "BACKGROUND") || strings.EqualFold(strings.TrimSpace(source.TemplateID), "VIDEO_BACKGROUND") {
			continue
		}
		candidates = append(candidates, overlayItemCandidate{index: index, source: source})
	}
	if len(candidates) == 0 {
		return RenderReference{}, nil
	}
	// The bound is published before the work starts, so the pool an operator
	// reads is the one this batch actually ran under (and not a later edit of
	// the config).
	// Back-pressure on queue depth (see resolveOverlayItemWorkers): this is
	// pipelining back-pressure, not GPU admission — gpu_lanes stays the sole
	// GPU authority.
	workers := resolveOverlayItemWorkers(e.itemRenderWorkers(), len(candidates))
	observability.OverlayItemRenderPoolSize.Set(float64(workers))
	results, err := concurrent.Map(ctx, candidates, workers, func(opCtx context.Context, _ int, candidate overlayItemCandidate) (overlayItemRenderResult, error) {
		// Measured concurrency, not assumed: the gauge rises exactly while a
		// render is awaiting its terminal state, so its peak is the pipelining
		// depth actually achieved.
		observability.OverlayItemRenderInFlight.Inc()
		defer observability.OverlayItemRenderInFlight.Dec()
		child, metadata, err := separateOverlayItemPlan(plan, candidate.source, candidate.index)
		if err != nil {
			return overlayItemRenderResult{}, err
		}
		ref, err := e.enqueueChrononPlan(opCtx, child, metadata)
		if err != nil {
			return overlayItemRenderResult{}, fmt.Errorf("render overlay item %q: %w", candidate.source.ID, err)
		}
		return overlayItemRenderResult{
			full: ref,
			item: OverlayItemRenderReference{
				ItemID: candidate.source.ID, JobID: ref.JobID, Status: ref.Status, Artifact: ref.Artifact,
			},
		}, nil
	})
	if err != nil {
		return RenderReference{}, err
	}
	items := make([]OverlayItemRenderReference, 0, len(results))
	for _, result := range results {
		items = append(items, result.item)
	}
	first := results[0].full
	first.Items = items
	return first, nil
}

type overlayItemPublicationMetadata struct {
	JobID            string
	ItemID           string
	ItemKind         string
	EntityID         string
	Text             string
	SourceStartUS    int64
	SourceEndUS      int64
	TargetDurationUS int64
}

const (
	overlayItemPaddingUS          int64 = 2_000_000
	maxOverlayItemDurationUS      int64 = 5_000_000
	maxCompositeOverlayDurationUS int64 = 8_000_000
)

func separateOverlayItemPlan(parent capoverlay.OverlayPlan, source capoverlay.OverlayItem, index int) (capoverlay.OverlayPlan, *overlayItemPublicationMetadata, error) {
	startUS, durationUS := source.StartUS, source.DurationUS
	if durationUS <= 0 {
		startUS = source.StartMs * 1000
		durationUS = (source.EndMs - source.StartMs) * 1000
	}
	if startUS < 0 || durationUS <= 0 {
		return capoverlay.OverlayPlan{}, nil, fmt.Errorf("overlay item %q has no positive certified duration", source.ID)
	}
	targetUS := durationUS + overlayItemPaddingUS
	maxDurationUS := maxOverlayItemDurationUS
	if len(source.ImageLayers) > 0 {
		// A composite's children carry staggered windows relative to the
		// parent. Preserve the entire (up to 5s + 3s mention gap) composition
		// instead of applying the ordinary 5s single-item cap.
		targetUS = durationUS
		maxDurationUS = maxCompositeOverlayDurationUS
	}
	if targetUS > maxDurationUS {
		targetUS = maxDurationUS
	}
	if targetUS <= 0 {
		return capoverlay.OverlayPlan{}, nil, fmt.Errorf("overlay item %q has invalid target duration", source.ID)
	}
	child := parent
	child.PlanID = overlayItemChildPlanID(parent.PlanID, index, source.ID)
	child.VideoID = child.PlanID
	child.DurationMS = (targetUS + 999) / 1000
	child.Fingerprint = ""
	child.Items = []capoverlay.OverlayItem{source}
	child.Items[0].StartUS = 0
	child.Items[0].DurationUS = targetUS
	child.Items[0].StartMs = 0
	child.Items[0].EndMs = child.DurationMS
	child.Items[0].RenderKey = ""
	if err := child.Validate(); err != nil {
		return capoverlay.OverlayPlan{}, nil, fmt.Errorf("build child plan for %q: %w", source.ID, err)
	}
	return child, &overlayItemPublicationMetadata{
		JobID:  firstNonEmpty(parent.DriveJobID, parent.PlanID),
		ItemID: source.ID, ItemKind: source.Kind, EntityID: source.EntityID,
		Text: source.Text, SourceStartUS: startUS, SourceEndUS: startUS + durationUS,
		TargetDurationUS: targetUS,
	}, nil
}

func overlayItemChildPlanID(parentPlanID string, index int, itemID string) string {
	full := fmt.Sprintf("%s:item:%03d:%s", parentPlanID, index, itemID)
	const maxPlanIDBytes = 180
	if len(full) <= maxPlanIDBytes {
		return full
	}
	// Byte-identical to the old direct sha256.Sum256 call: the digest SSOT
	// (godlike/06) centralises SHA-256 in internal/kernel/digest and the
	// delegation is verified golden old==new, so persisted plan IDs do not
	// change.
	sum := digest.SHA256String(full)
	const prefixLimit = 160
	var prefix strings.Builder
	for _, r := range full {
		if prefix.Len()+len(string(r)) > prefixLimit {
			break
		}
		prefix.WriteRune(r)
	}
	return strings.TrimRight(prefix.String(), ":-_") + "-" + sum[:16]
}
