package overlays

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"
)

// Animation preparation and finalization live here. Item admission and all
// collision checks run while overlays are still separate; chain fusion is the
// final transformation after the complete item set has been prepared.

// ApplyAnimationConsistency rotates duplicate phrase and number motions away.
// It mutates items in place and is idempotent: a second pass finds no
// duplicates and changes nothing.
func ApplyAnimationConsistency(items []capmodel.OverlayItem, input PlanInput) {
	ApplyAnimationConsistencyPinned(items, input, nil)
}

// ApplyAnimationConsistencyPinned is the policy-aware form. Indexes in pinned
// keep their explicit motion (a motion pin outranks automatic rotation) and
// are pre-registered as used so unpinned siblings rotate away.
func ApplyAnimationConsistencyPinned(items []capmodel.OverlayItem, input PlanInput, pinned map[int]bool) {
	EnsureDistinctPhraseMotionsPinned(items, input, pinned)
	EnsureDistinctNumberMotionsPinned(items, input.AnimationCounts, pinned)
}

// PrepareCompiledAnimationLayout runs the pre-policy stages of the compile
// boundary. The cascade must run before a policy can replace MotionParams: an
// explicit policy override owns its own entrance and is not re-shaped.
func PrepareCompiledAnimationLayout(items []capmodel.OverlayItem, input PlanInput, fpsNum, fpsDen int) []capmodel.OverlayItem {
	CascadeClusteredEntrances(items, fpsNum, fpsDen)
	ApplyAnimationConsistency(items, input)
	return items
}

// FinalizeCompiledAnimationLayout runs the post-policy stages of the compile
// boundary. EntityTimeline cards are resolved after the core BuildPlan call,
// so the image/date collision placement is repeated here on the final
// combined render set.
func FinalizeCompiledAnimationLayout(items []capmodel.OverlayItem, input PlanInput) []capmodel.OverlayItem {
	MoveImagesAsideForOverlappingDateCards(items, input.Width)
	items = capmodel.CoalesceChainedOverlays(items, input.AnimationCounts)
	return items
}

// FinalizePlannerAnimationLayout is the BuildPlan order. Images are moved
// aside before fusion so an image that later joins a chain carries its
// collision-free placement into the segment; the cascade runs last because
// chain containers are not shaped.
func FinalizePlannerAnimationLayout(items []capmodel.OverlayItem, input PlanInput, fpsNum, fpsDen int) []capmodel.OverlayItem {
	MoveImagesAsideForOverlappingDateCards(items, input.Width)
	ApplyAnimationConsistency(items, input)
	CascadeClusteredEntrances(items, fpsNum, fpsDen)
	return items
}

// ResolveAnimationSelections is the single-owner boundary that validates the
// shape of the final animation selection before sealing. It never resamples:
// the planner/compose owns the choice, and chains/composites must keep the
// authoritative motion on their segments/children.
func ResolveAnimationSelections(items []capmodel.OverlayItem) error {
	for i := range items {
		it := &items[i]
		if capmodel.IsChainItemKind(it.Kind) {
			if strings.TrimSpace(it.MotionID) != "" {
				return fmt.Errorf("animation selector: chain container %q must not carry MotionID %q (segments own it)", it.ID, it.MotionID)
			}
			segments, ok := capmodel.DecodeChainSegments(it.Params)
			if !ok || len(segments) < 2 {
				return fmt.Errorf("animation selector: chain container %q has no valid segments", it.ID)
			}
			for n, segment := range segments {
				if strings.TrimSpace(segment.MotionID) == "" {
					return fmt.Errorf("animation selector: chain %q segment %d has no MotionID", it.ID, n)
				}
				for _, layer := range segment.ImageLayers {
					if strings.TrimSpace(layer.MotionID) == "" {
						return fmt.Errorf("animation selector: chain %q segment %d image layer %q has no MotionID", it.ID, n, layer.ID)
					}
				}
			}
			continue
		}
		if it.Kind == string(capmodel.KindEntityImage) && len(it.ImageLayers) > 0 {
			if strings.TrimSpace(it.MotionID) != "" {
				return fmt.Errorf("animation selector: composite parent %q must not carry MotionID (child layers own it)", it.ID)
			}
			for _, layer := range it.ImageLayers {
				if strings.TrimSpace(layer.MotionID) == "" {
					return fmt.Errorf("animation selector: composite child %q:%q missing MotionID", it.ID, layer.ID)
				}
			}
			continue
		}
	}
	return nil
}
