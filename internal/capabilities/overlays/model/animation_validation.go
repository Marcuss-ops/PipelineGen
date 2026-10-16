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

func motionGroupsForItem(kind, template string, params map[string]any) []string {
	switch kind {
	case "text_phrase", "important_phrase", "bullet":
		return []string{"phrase.all", "phrase.short", "bullet"}
	case "number":
		template = strings.ToUpper(strings.TrimSpace(template))
		if strings.Contains(template, "DATE") {
			return []string{"entity.date"}
		}
		if strings.Contains(template, "METRIC") || strings.Contains(template, "STAT") {
			return []string{"entity.metric"}
		}
		return []string{"entity.date", "entity.metric", "phrase.all"}
	case "shape", "chart", "data":
		if params["chart_bar"] == true || params["chart_card"] == true || kind == "data" || kind == "chart" {
			return []string{"data"}
		}
		return []string{"phrase.all", "image.all", "data"}
	case "logo":
		return []string{"image.all", "image.lightleak", "phrase.all"}
	case "image", "entity_image", "image_popup", "product":
		return []string{"image.all", "image.lightleak", "phrase.all"}
	case "map":
		return []string{"image.map_centered"}
	default:
		return []string{"phrase.all", "phrase.short", "image.all", "image.lightleak", "entity.caption", "entity.date", "entity.metric", "data", "bullet"}
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
