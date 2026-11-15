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
	ApplyAnimationConsistencyPinned(items, input, pinnedPhraseMotionIndexes(items, input))
}

// pinnedPhraseMotionIndexes marks the items whose MotionID is the lane-wide
// pin from the payload. The final duplicate-rotation pass must leave those
// items alone: their sameness is the point of the pin, not an accidental
// collision.
func pinnedPhraseMotionIndexes(items []capmodel.OverlayItem, input PlanInput) map[int]bool {
	shortPin := ResolveDocumentaryPhraseMotion(strings.TrimSpace(input.ShortPhraseMotion))
	longPin := ResolveDocumentaryPhraseMotion(strings.TrimSpace(input.LongPhraseMotion))
	if shortPin == "" && longPin == "" {
		return nil
	}
	pinned := make(map[int]bool)
	for i := range items {
		if items[i].Kind != "text_phrase" || strings.TrimSpace(items[i].MotionID) == "" {
			continue
		}
		if items[i].Params != nil && items[i].Params["editorial_role"] == ChapterBulletRole {
			continue
		}
		words := len(strings.Fields(items[i].Text))
		if words >= 6 && longPin != "" && items[i].MotionID == longPin {
			pinned[i] = true
		}
		if words < 6 && shortPin != "" && items[i].MotionID == shortPin {
			pinned[i] = true
		}
	}
	return pinned
}

// ApplyAnimationConsistencyPinned is the policy-aware form. Indexes in pinned
// keep their explicit motion (a motion pin outranks automatic rotation) and
// are pre-registered as used so unpinned siblings rotate away.
func ApplyAnimationConsistencyPinned(items []capmodel.OverlayItem, input PlanInput, pinned map[int]bool) {
	EnsureDistinctPhraseMotionsPinned(items, input, pinned)
	EnsureDistinctNumberMotionsPinned(items, input, pinned)
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
	sceneEnds := make(map[string]int64, len(input.Scenes))
	for _, scene := range input.Scenes {
		if scene.EndUS > 0 {
			sceneEnds[scene.ID] = scene.EndUS
		}
	}
	items = capmodel.CoalesceChainedOverlaysWithSceneEnds(items, input.AnimationCounts, sceneEnds)
	if input.AnimationCounts["chain_disabled"] <= 0 {
		items = capmodel.CoalesceCaptionedEntityImageCards(items, sceneEnds)
	}
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
				return fmt.Errorf("animation selector: composite parent %q must not carry MotionID (the group or child layers own it)", it.ID)
			}
			if strings.TrimSpace(it.GroupMotionID) != "" {
				for _, layer := range it.ImageLayers {
					if strings.TrimSpace(layer.MotionID) != "" {
						return fmt.Errorf("animation selector: group-owned composite child %q:%q must not carry MotionID", it.ID, layer.ID)
					}
				}
				continue
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
