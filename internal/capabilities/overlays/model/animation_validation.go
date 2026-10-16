package model

import (
	"fmt"
	capanimreg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/animreg"
	"strings"
)

// validateAnimationReferences is the fail-fast boundary for every motion id
// leaving PipelineGen. It validates explicit item, caption, camera and child
// layer references against the same registry used by automatic selectors.
func validateAnimationReferences(item OverlayItem) error {
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	if id := strings.TrimSpace(item.MotionID); id != "" {
		groups := motionGroupsForItem(kind, item.TemplateID, item.Params)
		if !selectableInAnyGroup(id, groups...) {
			return fmt.Errorf("motion %q is unknown, retired, renderer-unsupported, or incompatible with item kind %q", id, item.Kind)
		}
	}
	if id := strings.TrimSpace(item.CaptionMotionID); id != "" && !capanimreg.AnimationSelectable(id, "entity.caption") && !capanimreg.AnimationSelectable(id, "phrase.all") {
		return fmt.Errorf("caption motion %q is unknown, retired, or renderer-unsupported", id)
	}
	if id := strings.TrimSpace(item.CameraMotionID); id != "" && !capanimreg.AnimationSelectable(id, "entity.sequence_camera") {
		return fmt.Errorf("camera motion %q is unknown, retired, or renderer-unsupported", id)
	}
	for _, layer := range item.ImageLayers {
		if id := strings.TrimSpace(layer.MotionID); id != "" && !selectableInAnyGroup(id, "image.all", "image.lightleak") {
			return fmt.Errorf("image layer %q motion %q is unknown, retired, or renderer-unsupported", layer.ID, id)
		}
		if id := strings.TrimSpace(layer.CaptionMotionID); id != "" && !capanimreg.AnimationSelectable(id, "entity.caption") && !capanimreg.AnimationSelectable(id, "phrase.all") {
			return fmt.Errorf("image layer %q caption motion %q is unknown, retired, or renderer-unsupported", layer.ID, id)
		}
	}
	return nil
}

// motionGroupsForItem resolves which registry groups an item of the given kind
// may sample a motion from. The kind→group mapping, the number-template rules
// and the chart upgrade are authored in the animation registry
// (`item_motion_groups`); this function only applies them, so adding a kind or
// remapping a group is a registry edit, not a code change.
func motionGroupsForItem(kind, template string, params map[string]any) []string {
	switch kind {
	case "number":
		return capanimreg.ItemNumberMotionGroups(template)
	case "shape", "chart", "data":
		for _, param := range capanimreg.ItemChartParams() {
			if params[param] == true {
				return capanimreg.ItemChartGroups()
			}
		}
		return capanimreg.ItemMotionGroups(kind)
	default:
		if groups := capanimreg.ItemMotionGroups(kind); groups != nil {
			return groups
		}
		return capanimreg.ItemDefaultMotionGroups()
	}
}

func selectableInAnyGroup(id string, groups ...string) bool {
	for _, group := range groups {
		if capanimreg.AnimationSelectable(id, group) {
			return true
		}
	}
	return false
}
