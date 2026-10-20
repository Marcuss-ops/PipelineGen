// Package scriptgeneration — overlay_category_filter.go owns the projection of
// a compiled OverlayItem onto the user-facing overlay category and the final
// category filter that honours the payload toggles. Split out of
// overlay_canvas.go to keep that file under the strict 600-LOC gate; same
// package, same behaviour.
package scriptgeneration

import (
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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
		if overlayItemCategoryDisabled(item, indexes[item.SceneID], canvas) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// filterScenePhrasesByCategory runs the phrase-category gate before BuildPlan
// applies its per-scene and run-level phrase budgets. Filtering only the
// materialized items afterward lets disabled title/bullet/phrase candidates
// consume those slots and leaves no candidate to backfill them.
func filterScenePhrasesByCategory(inputs []capabilityoverlay.SceneInput, scenes []Scene, canvas OverlayCanvasSpec) []capabilityoverlay.SceneInput {
	if len(inputs) == 0 || (len(canvas.OverlayCategories) == 0 && !hasSegmentOverlayCategories(canvas.OverlayCategorySegments)) {
		return inputs
	}
	indexes := make(map[string]int, len(scenes))
	for _, scene := range scenes {
		indexes[scene.ID] = scene.Index
	}
	for sceneIndex := range inputs {
		phrases := inputs[sceneIndex].Phrases
		if len(phrases) == 0 {
			continue
		}
		kept := phrases[:0]
		for _, phrase := range phrases {
			params := map[string]any{}
			if role := strings.TrimSpace(phrase.PresentationRole); role != "" {
				params["editorial_role"] = role
			}
			item := capabilityoverlay.OverlayItem{
				Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", SceneID: inputs[sceneIndex].ID, Params: params,
			}
			if !overlayItemCategoryDisabled(item, indexes[item.SceneID], canvas) {
				kept = append(kept, phrase)
			}
		}
		inputs[sceneIndex].Phrases = kept
	}
	return inputs
}

// overlayItemCategoryDisabled reports whether the item, or any child of a
// composed chain, belongs to a disabled overlay category. A chain container
// only names the coalescing family (phrase_chain, image_chain, ...), so the
// role of each child segment is invisible to overlayItemCategory: a plan
// that fused an editorial scene_bullet into a phrase chain would otherwise
// survive `bullets: disabled` and still render the bullet. Category
// disabling is atomic for a composed gesture — the whole container is dropped
// rather than rendering a partial chain whose siblings were authored as one
// visual unit.
func overlayItemCategoryDisabled(item capabilityoverlay.OverlayItem, sceneIndex int, canvas OverlayCanvasSpec) bool {
	if category, ok := overlayItemCategory(item); ok && !overlayCategoryEnabled(category, item.SceneID, sceneIndex, canvas) {
		return true
	}
	segments, ok := capabilityoverlay.DecodeChainSegments(item.Params)
	if !ok {
		return false
	}
	for _, segment := range segments {
		probe := capabilityoverlay.OverlayItem{Kind: segment.Kind, TemplateID: segment.TemplateID, Params: segment.Params}
		if category, ok := overlayItemCategory(probe); ok && !overlayCategoryEnabled(category, item.SceneID, sceneIndex, canvas) {
			return true
		}
	}
	return false
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
	// Chart comparison is atomic: the label card is Kind text_phrase /
	// Template IMPORTANT_PHRASE and the bars are Kind shape / Template
	// SHAPE, so template/kind alone miscategorize them (label → phrases,
	// bars → uncategorized passthrough). The Params flags chart_card /
	// chart_bar are canonical (see chart_plan.go BuildChartItems) and must
	// win before any generic text/shape rule, otherwise `phrases:
	// disabled` silently deletes chart labels (C1) and `charts: disabled`
	// leaks orphan bars (C2).
	case item.Params["chart_card"] == true || item.Params["chart_bar"] == true:
		return scriptpkg.OverlayCategoryCharts, true
	case strings.Contains(template, "date") || strings.Contains(template, "metric") || kind == "number":
		return scriptpkg.OverlayCategoryNumbers, true
	case item.Map != nil || kind == "map":
		return scriptpkg.OverlayCategoryMaps, true
	case strings.Contains(template, "chart") || kind == "chart_card" || kind == "chart_bar":
		return scriptpkg.OverlayCategoryCharts, true
	case item.Params["recap"] == true || strings.Contains(template, "recap"):
		return scriptpkg.OverlayCategoryRecaps, true
	// Editorial roles override the shared IMPORTANT_PHRASE template: the
	// request can independently enable bullets while disabling generic
	// phrases/titles, and the template name alone must not recategorize a
	// scene_bullet before its explicit role is examined.
	case role == "chapter_bullet":
		// A chapter opening rides the bullets category so the payload can
		// switch it off with the same toggle as ordinary bullets.
		return scriptpkg.OverlayCategoryBullets, true
	case role == "bullet" || role == "bullet_point" || role == "scene_bullet":
		return scriptpkg.OverlayCategoryBullets, true
	case role == "title" || role == "headline" || role == "scene_title":
		return scriptpkg.OverlayCategoryTitles, true
	case strings.Contains(template, "title"):
		return scriptpkg.OverlayCategoryTitles, true
	case strings.Contains(template, "bullet"):
		return scriptpkg.OverlayCategoryBullets, true
	case strings.Contains(template, "quote") || role == "quote":
		return scriptpkg.OverlayCategoryQuotes, true
	case strings.Contains(kind, "brand") || role == "brand":
		return scriptpkg.OverlayCategoryBrandText, true
	case kind == "text_phrase" || strings.Contains(template, "phrase"):
		return scriptpkg.OverlayCategoryPhrases, true
	case kind == string(capabilityoverlay.KindEntityCard) && !isImageOverlayItem(item):
		// A text-only named entity card is NOT imagery, whatever entity type it
		// names: the planner caps these cards per scene and the payload controls
		// them through the `entities_with_text` group limit, exactly like the
		// group classifier (overlayGroupOf) already separates them from `images`.
		// Categorizing them as images would let an `images: disabled` toggle — a
		// control for photo overlays — silently delete every place, person and
		// organization name card in the run, and an image-backed card of the
		// SAME entity would still be filtered by that toggle.
		return "", false
	case strings.Contains(kind, "image") || kind == "entity_card" || kind == "product" || kind == "logo":
		return scriptpkg.OverlayCategoryImages, true
	default:
		return "", false
	}
}
