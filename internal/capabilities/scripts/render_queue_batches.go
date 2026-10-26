package scriptgeneration

// render_queue_batches.go owns the composition helpers of the manifest
// timeline (OVERLAY-MANIFEST-CONTRACT.md): the work identity, the wire-rounded
// window end and the fused child plan that renders every ordinary overlay
// item of a plan as ONE certified artifact. The old costed window fusion and
// P4 packed-segment planner were removed by the manifest-timeline cutover:
// ordinary items always compose one artifact per plan timeline, so per-item
// output packing no longer exists. Maps and chain items keep their dedicated
// per-item paths (render_queue_items_batch.go).

import (
	"fmt"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// overlayBatchWork is one Chronon invocation: the manifest timeline
// composition. Maps and chain items never enter it — their dynamic renderers
// have separate admission and asset lifecycles.
type overlayBatchWork struct {
	id         string
	sceneID    string
	startUS    int64
	endUS      int64
	candidates []overlayItemCandidate
}

func overlayBatchWireEndUS(startUS, endUS int64) int64 {
	durationUS := endUS - startUS
	if durationUS <= 0 {
		return endUS
	}
	return startUS + ((durationUS+999)/1000)*1000
}

func overlayItemWindow(item capoverlay.OverlayItem) (int64, int64) {
	return item.StartUSValue(), item.EndUSValue()
}

// overlayCandidateRenderWindow shares the same duration/timing normalization
// used by the single-item renderer, including phrase tails and composite spans.
func overlayCandidateRenderWindow(plan capoverlay.OverlayPlan, candidate overlayItemCandidate) (int64, int64) {
	if _, metadata, err := separateOverlayItemPlan(plan, candidate.source, candidate.index); err == nil && metadata != nil && metadata.TargetDurationUS > 0 {
		return metadata.SourceStartUS, metadata.SourceStartUS + metadata.TargetDurationUS
	}
	return overlayItemWindow(candidate.source)
}

// overlayItemMayCompileLinkedCaption reports whether the item can compile a
// caption layer linked to one of its images (the worker wire's
// CaptionForImageID). RenderingGen compiles linked captions only from the
// explicit caption fields — item.EntityCaption (semantic image caption) and
// ImageLayers[].Caption (composite image children); an item without either
// cannot join the collision class no matter its kind.
func overlayItemMayCompileLinkedCaption(item capoverlay.OverlayItem) bool {
	if strings.TrimSpace(item.EntityCaption) != "" {
		return true
	}
	for _, layer := range item.ImageLayers {
		if strings.TrimSpace(layer.Caption) != "" {
			return true
		}
	}
	return false
}

func overlayBatchIdentity(plan capoverlay.OverlayPlan, work overlayBatchWork) string {
	// The schema tag keeps composition identities in their historical digest
	// domain: renaming it would orphan every cached timeline artifact.
	schema := "pipelinegen.overlay-window-batch.v1"
	parts := []string{schema, strings.TrimSpace(plan.PlanID), strings.TrimSpace(work.sceneID), fmt.Sprint(work.startUS), fmt.Sprint(work.endUS)}
	for _, candidate := range work.candidates {
		item := candidate.source
		itemKey := strings.ToLower(strings.TrimSpace(item.RenderKey))
		if itemKey == "" {
			itemKey = capoverlay.ComputeRenderKey(plan, item)
		}
		// BatchID is a durable semantic reference used by publication and the
		// final-job handoff. Include content identity as well as membership and
		// timing so editing an item under a reused PlanID cannot alias an older
		// batch receipt. The render cache independently includes the same item
		// keys plus its complete plan context.
		parts = append(parts, strings.TrimSpace(item.ID), itemKey)
	}
	return digest.SHA256String(strings.Join(parts, "\x00"))
}

// fusedOverlayBatchPlan composes the manifest-timeline child plan: every
// candidate's item at its plan-absolute window on the parent's canvas (the
// artifact is anchored at plan t=0). One candidate is valid — a single voice
// still composes one manifest timeline artifact.
func fusedOverlayBatchPlan(parent capoverlay.OverlayPlan, work overlayBatchWork) (capoverlay.OverlayPlan, error) {
	if len(work.candidates) == 0 || work.endUS <= work.startUS {
		return capoverlay.OverlayPlan{}, fmt.Errorf("overlay batch %q is not a positive window", work.id)
	}
	child, _, err := separateOverlayItemPlan(parent, work.candidates[0].source, work.candidates[0].index)
	if err != nil {
		return capoverlay.OverlayPlan{}, err
	}
	items := make([]capoverlay.OverlayItem, 0, len(work.candidates))
	endUS := work.endUS
	for _, candidate := range work.candidates {
		// Maps and chain items never enter the composition: they keep their
		// dedicated render paths (partitionOverlayRenderWork). The manifest
		// timeline is plan-scoped by design — one certified artifact covers
		// every scene's items on the plan-absolute frame grid
		// (OVERLAY-MANIFEST-CONTRACT.md §3) — so scene-local batch boundaries
		// do not apply here.
		if candidate.source.Map != nil {
			return capoverlay.OverlayPlan{}, fmt.Errorf("overlay batch %q contains a map item", work.id)
		}
		itemPlan, metadata, itemErr := separateOverlayItemPlan(parent, candidate.source, candidate.index)
		if itemErr != nil {
			return capoverlay.OverlayPlan{}, itemErr
		}
		if metadata.SourceStartUS < work.startUS {
			return capoverlay.OverlayPlan{}, fmt.Errorf("overlay batch %q has item %q before its start", work.id, candidate.source.ID)
		}
		item := itemPlan.Items[0]
		item.StartUS = metadata.SourceStartUS - work.startUS
		// P5/P4 window contract (OVERLAY-MANIFEST-CONTRACT.md §1): the item
		// occupies exactly [in_us, out_us) = its LITERAL plan window, because
		// the handoff projects that segment onto the artifact's frames (§3).
		// TargetDurationUS is the padded standalone-clip target (entrance tail
		// + settled hold, see separateOverlayItemPlan): using it here made
		// fused items persist past their windows and overlap later items in
		// the shared timeline (live A/B 2026-10-24: visible text stacking).
		item.DurationUS = metadata.SourceEndUS - metadata.SourceStartUS
		item.StartMs = item.StartUS / 1000
		item.EndMs = (item.StartUS + item.DurationUS + 999) / 1000
		item.RenderKey = ""
		if item.StartUS+item.DurationUS > endUS {
			endUS = item.StartUS + item.DurationUS
		}
		items = append(items, item)
	}
	child.PlanID = overlayItemChildPlanID(parent.PlanID, work.candidates[0].index, "batch-"+work.id)
	child.VideoID = child.PlanID
	child.Fingerprint = ""
	child.Items = items
	child.DurationMS = (endUS - work.startUS + 999) / 1000
	if err := child.Validate(); err != nil {
		return capoverlay.OverlayPlan{}, fmt.Errorf("validate fused overlay batch %q: %w", work.id, err)
	}
	return child, nil
}
