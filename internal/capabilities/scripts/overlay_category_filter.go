package scriptgeneration

import (
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func hasSegmentOverlayCategories(segments []scriptpkg.ScriptSegment) bool {
	for _, segment := range segments {
		if len(segment.OverlayCategories) > 0 {
			return true
		}
	}
	return false
}

func filterOverlayItemsByCategory(items []capabilityoverlay.OverlayItem, scenes []Scene, canvas OverlayCanvasSpec) []capabilityoverlay.OverlayItem {
	if len(canvas.OverlayCategories) == 0 && !hasSegmentOverlayCategories(canvas.OverlayCategorySegments) {
		return items
	}
	indexes := make(map[string]int, len(scenes))
	for _, scene := range scenes {
		indexes[scene.ID] = scene.Index
	}
	out := make([]capabilityoverlay.OverlayItem, 0, len(items))
	for _, item := range items {
		category, ok := overlayItemCategory(item)
		if !ok || overlayCategoryEnabled(category, item.SceneID, indexes[item.SceneID], canvas) {
			out = append(out, item)
		}
	}
	return out
}

func overlayCategoryEnabled(category scriptpkg.OverlayCategory, sceneID string, sceneIndex int, canvas OverlayCanvasSpec) bool {
	value := canvas.OverlayCategories[category]
	for i, segment := range canvas.OverlayCategorySegments {
		id := strings.TrimSpace(segment.ID)
		if id != "" && id == sceneID {
			if override := segment.OverlayCategories[category]; override != "" && override != scriptpkg.ToggleDefault {
				value = override
			}
			break
		}
		if i == sceneIndex && id == "" {
			if override := segment.OverlayCategories[category]; override != "" && override != scriptpkg.ToggleDefault {
				value = override
			}
		}
	}
	return value != scriptpkg.ToggleDisabled
}

func overlayItemCategory(item capabilityoverlay.OverlayItem) (scriptpkg.OverlayCategory, bool) {
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	template := strings.ToLower(strings.TrimSpace(item.TemplateID))
	role, _ := item.Params["editorial_role"].(string)
	role = strings.ToLower(strings.TrimSpace(role))
	switch {
	case strings.Contains(template, "date") || strings.Contains(template, "metric") || kind == "number":
		return scriptpkg.OverlayCategoryNumbers, true
	case item.Map != nil || kind == "map":
		return scriptpkg.OverlayCategoryMaps, true
	case strings.Contains(template, "chart") || kind == "chart_card" || kind == "chart_bar":
		return scriptpkg.OverlayCategoryCharts, true
	case item.Params["recap"] == true || strings.Contains(template, "recap"):
		return scriptpkg.OverlayCategoryRecaps, true
	case role == "title" || role == "headline" || role == "scene_title" || strings.Contains(template, "title"):
		return scriptpkg.OverlayCategoryTitles, true
	case role == "bullet" || role == "bullet_point" || role == "scene_bullet" || strings.Contains(template, "bullet"):
		return scriptpkg.OverlayCategoryBullets, true
	case strings.Contains(template, "quote") || role == "quote":
		return scriptpkg.OverlayCategoryQuotes, true
	case strings.Contains(kind, "brand") || role == "brand":
		return scriptpkg.OverlayCategoryBrandText, true
	case kind == "text_phrase" || strings.Contains(template, "phrase"):
		return scriptpkg.OverlayCategoryPhrases, true
	case strings.Contains(kind, "image") || kind == "entity_card" || kind == "product" || kind == "logo":
		return scriptpkg.OverlayCategoryImages, true
	default:
		return "", false
	}
}
