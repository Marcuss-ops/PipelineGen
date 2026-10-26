package scriptgeneration

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// certifiedSegmentReferences validates the worker-owned independent outputs
// before any publication or final-job handoff. Overlapping editorial batches
// have no segments and continue to use their single composite artifact.
func certifiedSegmentReferences(artifact *RenderArtifact, plan capoverlay.OverlayPlan, jobID string) ([]OverlayItemRenderReference, error) {
	if artifact == nil || len(artifact.BatchSegments) == 0 {
		return nil, nil
	}
	byID := make(map[string]OverlayItemRenderReference, len(artifact.BatchSegments))
	for _, ref := range artifact.BatchSegments {
		a := ref.Artifact
		if _, duplicate := byID[ref.ItemID]; duplicate || ref.ItemID == "" || a == nil {
			return nil, fmt.Errorf("invalid or duplicate batch segment %q", ref.ItemID)
		}
		digest, err := hex.DecodeString(a.SHA256)
		if err != nil || len(digest) != 32 || a.StorageKey != a.SHA256 || a.URL == "" || a.SizeBytes <= 0 || a.FrameCount <= 0 || a.DurationUS <= 0 || len(a.OutputFacts) == 0 {
			return nil, fmt.Errorf("batch segment %q has incomplete certification", ref.ItemID)
		}
		ref.JobID = jobID
		byID[ref.ItemID] = ref
	}
	refs := make([]OverlayItemRenderReference, 0, len(byID))
	for _, item := range plan.Items {
		if item.Kind == "background" {
			continue
		}
		ref, ok := byID[item.ID]
		if !ok {
			return nil, fmt.Errorf("missing batch segment for item %q", item.ID)
		}
		if ref.Artifact.Width != plan.Width || ref.Artifact.Height != plan.Height || ref.Artifact.FPSNum <= 0 || ref.Artifact.FPSDen <= 0 || ref.Artifact.FPSNum*plan.FPSDen != plan.FPSNum*ref.Artifact.FPSDen {
			return nil, fmt.Errorf("batch segment %q canvas/fps mismatch", item.ID)
		}
		refs = append(refs, ref)
		delete(byID, item.ID)
	}
	if len(byID) != 0 {
		return nil, fmt.Errorf("batch contains unexpected segment identities")
	}
	return refs, nil
}

// overlayBatchWork is one Chronon invocation. Batch windows are scene-local;
// maps remain singleton work because their dynamic renderer has a separate
// admission and asset lifecycle.
type overlayBatchWork struct {
	id         string
	sceneID    string
	startUS    int64
	endUS      int64
	candidates []overlayItemCandidate
}

// planOverlayBatchWork fuses windows only when the render cost model predicts
// a saving. A gap can be fused only when the parent plan supplies a looping
// video background to fill it; otherwise the fused clip would invent blank
// frames between semantic overlays.
func planOverlayBatchWork(plan capoverlay.OverlayPlan, candidates []overlayItemCandidate) []overlayBatchWork {
	ordered := append([]overlayItemCandidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].source.SceneID != ordered[j].source.SceneID {
			return ordered[i].source.SceneID < ordered[j].source.SceneID
		}
		si, _ := overlayItemWindow(ordered[i].source)
		sj, _ := overlayItemWindow(ordered[j].source)
		if si != sj {
			return si < sj
		}
		return ordered[i].source.ID < ordered[j].source.ID
	})
	// A color/image background cannot safely fill a sparse interval in a
	// mode=replace handoff. OverlayBackground is a deliberately global plate.
	// Source is different: the
	// wire has no source-time offset, so copying a full source into a window
	// batch would restart it at frame zero and show the wrong footage.
	stableBackground := plan.Source == nil && plan.Background != nil && plan.Background.Loop && strings.EqualFold(strings.TrimSpace(plan.Background.Kind), "video")
	// A final-job artifact is handed to a frame-replacing master. A generic
	// looping Background is not proof that it is the canonical clip selected
	// for this window. OverlayPlan currently has no per-window source binding,
	// so leave every such plan per-item until that binding is part of the
	// contract and is passed through the same canonical selector.
	if plan.RequireDriveBeforeReturn {
		stableBackground = false
	}
	const (
		// Least-squares fit to the 39 non-map per-item samples from the v15
		// golden (run_da494991-1b8d-47c6-aaac-dd9ec0473a4a): 1,035.8 ms fixed
		// service + 15.369 ms/output frame. R² was 0.317, so cost wins require
		// at least a 10% modeled saving; same-mix batch runs still need to verify
		// this approximation before the 100/min gate can close.
		setupMS     = 1035.8
		frameMS     = 15.369
		maxItems    = 12
		maxWindowUS = int64(30_000_000)
	)
	fpsNum, fpsDen := plan.FPSNum, plan.FPSDen
	if fpsNum <= 0 || fpsDen <= 0 {
		fpsNum, fpsDen = 24, 1
	}
	var works []overlayBatchWork
	for i := 0; i < len(ordered); {
		first := ordered[i]
		start, end := overlayCandidateRenderWindow(plan, first)
		work := overlayBatchWork{sceneID: first.source.SceneID, startUS: start, endUS: end, candidates: []overlayItemCandidate{first}}
		j := i + 1
		for j < len(ordered) && len(work.candidates) < maxItems && plan.Source == nil && !plan.RequireDriveBeforeReturn && first.source.Map == nil && strings.TrimSpace(work.sceneID) != "" && !capoverlay.IsChainItemKind(first.source.Kind) {
			candidate := ordered[j]
			if candidate.source.Map != nil || capoverlay.IsChainItemKind(candidate.source.Kind) || strings.TrimSpace(candidate.source.SceneID) != strings.TrimSpace(work.sceneID) {
				break
			}
			// A fused window forces its members into one shared frame timeline,
			// where RenderingGen's fitted entity-caption gate compares every
			// image-linked caption against every other. Two caption-bearing
			// members whose captions fit into overlapping canvas bounds are
			// rejected deterministically by the worker (golden v21, batch
			// ef106785…), even though the same items render cleanly per item —
			// per-item renders carry exactly one caption each and cannot collide.
			// The planner cannot predict the fitted caption geometry (fitting
			// runs on materialized aspect ratios inside the worker), so a batch
			// admits at most one caption-bearing member; text-only members fuse
			// freely around it.
			if overlayItemMayCompileLinkedCaption(candidate.source) && batchHasCaptionBearing(work.candidates) {
				break
			}
			nextStart, nextEnd := overlayCandidateRenderWindow(plan, candidate)
			gap := nextStart - work.endUS
			if gap < 0 {
				gap = 0
			}
			if gap > 0 && !stableBackground {
				break
			}
			unionEnd := maxInt64(work.endUS, nextEnd)
			if unionEnd-start > maxWindowUS {
				break
			}
			trial := append(append([]overlayItemCandidate(nil), work.candidates...), candidate)
			if !overlayBatchCostWins(plan, trial, start, unionEnd, setupMS, frameMS, fpsNum, fpsDen) {
				break
			}
			work.candidates = append(work.candidates, candidate)
			work.endUS = unionEnd
			j++
		}
		// The temporal sort is only for grouping. Rendering order carries the
		// parent's z-order/editorial order and must survive composition.
		sort.SliceStable(work.candidates, func(a, b int) bool {
			return work.candidates[a].index < work.candidates[b].index
		})
		// RenderingGen's wire timing is millisecond-based. Stamp the same
		// rounded end into identity and the durable batch reference so recovery
		// can recompute identity from the exact interval that was rendered.
		work.endUS = overlayBatchWireEndUS(work.startUS, work.endUS)
		work.id = overlayBatchIdentity(plan, work)
		works = append(works, work)
		i += len(work.candidates)
	}
	return works
}

func overlayBatchWireEndUS(startUS, endUS int64) int64 {
	durationUS := endUS - startUS
	if durationUS <= 0 {
		return endUS
	}
	return startUS + ((durationUS+999)/1000)*1000
}

func overlayBatchCostWins(plan capoverlay.OverlayPlan, candidates []overlayItemCandidate, batchStartUS, batchEndUS int64, setupMS, frameMS float64, fpsNum, fpsDen int) bool {
	if len(candidates) < 2 || batchEndUS <= batchStartUS {
		return false
	}
	separateMS := float64(len(candidates)) * setupMS
	for _, candidate := range candidates {
		startUS, endUS := overlayCandidateRenderWindow(plan, candidate)
		durationUS := endUS - startUS
		frames := ceilFrames(durationUS, fpsNum, fpsDen)
		separateMS += float64(frames) * frameMS
	}
	batchFrames := ceilFrames(batchEndUS-batchStartUS, fpsNum, fpsDen)
	batchMS := setupMS + float64(batchFrames)*frameMS
	return batchMS < separateMS*0.9
}

func overlayItemWindow(item capoverlay.OverlayItem) (int64, int64) {
	return item.StartUSValue(), item.EndUSValue()
}

// overlayCandidateRenderWindow shares the same duration/timing normalization
// used by the single-item renderer, including phrase tails and composite spans.
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

func batchHasCaptionBearing(candidates []overlayItemCandidate) bool {
	for _, candidate := range candidates {
		if overlayItemMayCompileLinkedCaption(candidate.source) {
			return true
		}
	}
	return false
}

func overlayCandidateRenderWindow(plan capoverlay.OverlayPlan, candidate overlayItemCandidate) (int64, int64) {
	if _, metadata, err := separateOverlayItemPlan(plan, candidate.source, candidate.index); err == nil && metadata != nil && metadata.TargetDurationUS > 0 {
		return metadata.SourceStartUS, metadata.SourceStartUS + metadata.TargetDurationUS
	}
	return overlayItemWindow(candidate.source)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func ceilFrames(durationUS int64, fpsNum, fpsDen int) int64 {
	if durationUS <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	denom := int64(1_000_000 * fpsDen)
	return (durationUS*int64(fpsNum) + denom - 1) / denom
}

func overlayBatchIdentity(plan capoverlay.OverlayPlan, work overlayBatchWork) string {
	parts := []string{"pipelinegen.overlay-window-batch.v1", strings.TrimSpace(plan.PlanID), strings.TrimSpace(work.sceneID), fmt.Sprint(work.startUS), fmt.Sprint(work.endUS)}
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

func fusedOverlayBatchPlan(parent capoverlay.OverlayPlan, work overlayBatchWork) (capoverlay.OverlayPlan, error) {
	if len(work.candidates) < 2 || work.endUS <= work.startUS {
		return capoverlay.OverlayPlan{}, fmt.Errorf("overlay batch %q is not a multi-item positive window", work.id)
	}
	child, _, err := separateOverlayItemPlan(parent, work.candidates[0].source, work.candidates[0].index)
	if err != nil {
		return capoverlay.OverlayPlan{}, err
	}
	items := make([]capoverlay.OverlayItem, 0, len(work.candidates))
	endUS := work.endUS
	for _, candidate := range work.candidates {
		if candidate.source.Map != nil || strings.TrimSpace(candidate.source.SceneID) != strings.TrimSpace(work.sceneID) {
			return capoverlay.OverlayPlan{}, fmt.Errorf("overlay batch %q crosses a map or scene boundary", work.id)
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
