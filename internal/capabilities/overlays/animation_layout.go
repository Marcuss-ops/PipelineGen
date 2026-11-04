package overlays

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"
)

// animation_layout.go is the single owner of the order in which the producer
// settles nearby animations. BuildPlan and the script-generation compile
// boundary used to re-implement the same passes (duplicate-motion rotation,
// image/date collision placement, chain fusion, entrance cascade) in two
// different files. Those sequences now live here:
//
//	planner  : move images -> rotate duplicates -> fuse chains -> cascade
//	compiled : cascade + rotate (before policies) -> fuse chains -> move images
//
// The two orders are deliberately different. BuildPlan runs before nearby
// images are composed, while the compile boundary re-runs the layout on the
// combined item set after the policy projection. Do not copy a pass sequence
// into a caller: add a stage here instead.

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
	items = capmodel.CoalesceChainedOverlays(items, input.AnimationCounts)
	MoveImagesAsideForOverlappingDateCards(items)
	return items
}

// FinalizePlannerAnimationLayout is the BuildPlan order. Images are moved
// aside before fusion so an image that later joins a chain carries its
// collision-free placement into the segment; the cascade runs last because
// chain containers are not shaped.
func FinalizePlannerAnimationLayout(items []capmodel.OverlayItem, input PlanInput, fpsNum, fpsDen int) []capmodel.OverlayItem {
	MoveImagesAsideForOverlappingDateCards(items)
	ApplyAnimationConsistency(items, input)
	items = capmodel.CoalesceChainedOverlays(items, input.AnimationCounts)
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
