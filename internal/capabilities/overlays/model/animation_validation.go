package model

import (
	"fmt"
	"strings"

	capanimreg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/animreg"
)

// validateAnimationReferences is the fail-fast boundary for every motion id
// leaving PipelineGen. It validates explicit item, caption, camera and child
// layer references against the same registry used by automatic selectors.
func validateAnimationReferences(item OverlayItem) error {
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	if id := strings.TrimSpace(item.MotionID); id != "" {
		groups := motionGroupsForItem(kind, item.TemplateID, item.Params)
		// Entity cards are text-only unless the resolved item carries image
		// material. RenderingGen lowers an image-backed location/organization/
		// concept through its image layer, so its motion must be certified for
		// the image target as well as the semantic entity kind.
		if isImageItemKind(kind) || (isEntityItemKind(kind) && (len(item.AssetRefs) > 0 || len(item.ImageLayers) > 0)) {
			groups = append(groups, "image.recipe")
			groups = append(groups, capanimreg.ItemMotionGroups("entity_image")...)
			groups = append(groups, "authored.image_premium_v1")
		}
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
	if id := strings.TrimSpace(item.GroupMotionID); id != "" {
		if len(item.ImageLayers) < 2 {
			return fmt.Errorf("group motion %q requires a composite with at least two image layers", id)
		}
		group := fmt.Sprintf("image.group.count_%d", len(item.ImageLayers))
		if !capanimreg.AnimationSelectable(id, group) {
			return fmt.Errorf("group motion %q is unknown, retired, renderer-unsupported, or incompatible with %s", id, group)
		}
		if strings.TrimSpace(item.MotionID) != "" || strings.TrimSpace(item.CameraMotionID) != "" {
			return fmt.Errorf("group motion %q is mutually exclusive with item and camera motions", id)
		}
		for _, layer := range item.ImageLayers {
			if strings.TrimSpace(layer.MotionID) != "" {
				return fmt.Errorf("group motion %q is mutually exclusive with child layer motions (layer %q)", id, layer.ID)
			}
		}
	}
	for _, layer := range item.ImageLayers {
		if id := strings.TrimSpace(layer.MotionID); id != "" && !selectableInAnyGroup(id, "image.all", "image.lightleak", "image.recipe") {
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

func isImageItemKind(kind string) bool {
	switch kind {
	case "image", "entity_image", "image_popup", "product", "logo":
		return true
	default:
		return false
	}
}

func isEntityItemKind(kind string) bool {
	switch kind {
	case "entity_card", "location", "organization", "concept":
		return true
	default:
		return false
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
