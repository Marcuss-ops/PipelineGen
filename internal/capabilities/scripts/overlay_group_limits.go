package scriptgeneration

import (
	"fmt"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// Overlay group keys accepted by overlay_group_limits.
const (
	overlayGroupMaps             = "maps"
	overlayGroupShortPhrases     = "short_phrases"
	overlayGroupLongPhrases      = "long_phrases"
	overlayGroupEntitiesWithText = "entities_with_text"
	overlayGroupImages           = "images"
)

var overlayGroupKeys = map[string]bool{
	overlayGroupMaps:             true,
	overlayGroupShortPhrases:     true,
	overlayGroupLongPhrases:      true,
	overlayGroupEntitiesWithText: true,
	overlayGroupImages:           true,
}

func cloneOverlayGroupLimits(src map[string]int) map[string]int {
	if src == nil {
		return nil
	}
	dst := make(map[string]int, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// validateOverlayGroupLimits fails closed on unknown keys or negative caps.
func validateOverlayGroupLimits(limits map[string]int) error {
	for key, value := range limits {
		if !overlayGroupKeys[key] {
			return fmt.Errorf("overlay_group_limits has unsupported group %q", key)
		}
		if value < 0 {
			return fmt.Errorf("overlay_group_limits[%q] must be >= 0, got %d", key, value)
		}
	}
	return nil
}

// overlayGroupOf assigns one overlay to exactly one group. Maps, images,
// entity cards and phrases are disjoint; short phrases use the exact-cardinality
// short recipes, every other grounded phrase counts as long.
func overlayGroupOf(item capabilityoverlay.OverlayItem) string {
	switch {
	case item.Kind == "map" && item.Map != nil:
		return overlayGroupMaps
	case isImageOverlayItem(item):
		return overlayGroupImages
	case entityCardKind(capabilityoverlay.OverlayKind(item.Kind)):
		return overlayGroupEntitiesWithText
	case item.Kind == "text_phrase" || item.Kind == string(capabilityoverlay.KindImportantPhrase):
		if capabilityoverlay.IsShortPhraseRecipe(item.MotionID) {
			return overlayGroupShortPhrases
		}
		return overlayGroupLongPhrases
	}
	return ""
}

// applyOverlayGroupLimits keeps, per group in plan order, at most the
// configured number of overlays. Present zero drops the group; absent keys
// and ungrouped items pass through unchanged.
func applyOverlayGroupLimits(items []capabilityoverlay.OverlayItem, limits map[string]int) []capabilityoverlay.OverlayItem {
	if len(limits) == 0 {
		return items
	}
	kept := make(map[string]int, len(limits))
	out := items[:0]
	for _, item := range items {
		group := overlayGroupOf(item)
		limit, capped := limits[group]
		if group == "" || !capped {
			out = append(out, item)
			continue
		}
		if kept[group] >= limit {
			continue
		}
		kept[group]++
		out = append(out, item)
	}
	return out
}

// overlayGroupLimit returns the cap for a group and whether it was set.
func overlayGroupLimit(limits map[string]int, group string) (int, bool) {
	value, ok := limits[group]
	return value, ok
}
