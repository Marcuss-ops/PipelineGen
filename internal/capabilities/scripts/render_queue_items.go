package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// defaultSeparateItemRenderWorkers is how many per-item overlay renders may be
// in flight at once when the caller does not tune the pool. It matches the
// canonical render-call concurrency this codebase already uses elsewhere
// (localization.DefaultRenderConcurrency), and it is deliberately the same
// order of magnitude as the measured worker ceiling rather than a large fan-out:
// the point is to stop serialising the per-item pre/post chain, not to flood the
// queue with GPU work the host cannot run.
const defaultSeparateItemRenderWorkers = 4

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
// an independent short video. The returned reference is the first artifact
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
	workers := e.itemRenderWorkers()
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
	overlayItemPaddingUS     int64 = 2_000_000
	maxOverlayItemDurationUS int64 = 5_000_000
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
	if targetUS > maxOverlayItemDurationUS {
		targetUS = maxOverlayItemDurationUS
	}
	if targetUS <= 0 {
		return capoverlay.OverlayPlan{}, nil, fmt.Errorf("overlay item %q has invalid target duration", source.ID)
	}
	child := parent
	child.PlanID = fmt.Sprintf("%s:item:%03d:%s", parent.PlanID, index, source.ID)
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
