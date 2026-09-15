package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// enqueueSeparateOverlayItems renders and publishes each semantic overlay as
// an independent short video. The returned reference is the first artifact
// for backwards-compatible callers; every artifact is published by the same
// application-owned publisher and receives its own receipt.
func (e *QueueRenderEnqueuer) enqueueSeparateOverlayItems(ctx context.Context, plan capoverlay.OverlayPlan) (RenderReference, error) {
	var first RenderReference
	items := make([]OverlayItemRenderReference, 0, len(plan.Items))
	produced := 0
	for index, source := range plan.Items {
		// A background is a canvas primitive, not a user-facing overlay. It is
		// copied into each child plan when the parent uses the first-class
		// Background field; a legacy background item must never become its own
		// Drive video.
		if strings.EqualFold(strings.TrimSpace(source.TemplateID), "BACKGROUND") || strings.EqualFold(strings.TrimSpace(source.TemplateID), "VIDEO_BACKGROUND") {
			continue
		}
		child, metadata, err := separateOverlayItemPlan(plan, source, index)
		if err != nil {
			return RenderReference{}, err
		}
		ref, err := e.enqueueChrononPlan(ctx, child, metadata)
		if err != nil {
			return RenderReference{}, fmt.Errorf("render overlay item %q: %w", source.ID, err)
		}
		if produced == 0 {
			first = ref
		}
		items = append(items, OverlayItemRenderReference{
			ItemID: source.ID, JobID: ref.JobID, Status: ref.Status, Artifact: ref.Artifact,
		})
		produced++
	}
	if produced == 0 {
		return RenderReference{}, nil
	}
	first.Items = items
	return first, nil
}

type overlayItemPublicationMetadata struct {
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
		ItemID: source.ID, ItemKind: source.Kind, EntityID: source.EntityID,
		Text: source.Text, SourceStartUS: startUS, SourceEndUS: startUS + durationUS,
		TargetDurationUS: targetUS,
	}, nil
}
