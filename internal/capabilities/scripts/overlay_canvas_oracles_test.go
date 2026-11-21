package scriptgeneration

import (
	"fmt"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

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

func dropCategoryDisabledCandidates(in *capabilityoverlay.SceneInput, sceneID string, sceneIndex int, canvas OverlayCanvasSpec) *capabilityoverlay.SceneInput {
	if in == nil {
		return nil
	}
	out := *in
	if !overlayCategoryEnabled(scriptpkg.OverlayCategoryPhrases, sceneID, sceneIndex, canvas) {
		out.Phrases = nil
	}
	chartsEnabled := canvas.EnableCharts && overlayCategoryEnabled(scriptpkg.OverlayCategoryCharts, sceneID, sceneIndex, canvas)
	if !overlayCategoryEnabled(scriptpkg.OverlayCategoryNumbers, sceneID, sceneIndex, canvas) && !chartsEnabled {
		out.Numbers = nil
	}
	if !overlayCategoryEnabled(scriptpkg.OverlayCategoryImages, sceneID, sceneIndex, canvas) {
		out.Images = nil
		out.Products = nil
		out.Logos = nil
	}
	if !overlayCategoryEnabled(scriptpkg.OverlayCategoryMaps, sceneID, sceneIndex, canvas) {
		out.Maps = nil
	}
	if !overlayCategoryEnabled(scriptpkg.OverlayCategoryBrandText, sceneID, sceneIndex, canvas) {
		out.BrandTexts = nil
	}
	if !chartsEnabled {
		out.ChartNumbers = nil
	}
	if len(out.Phrases) == 0 && len(out.Numbers) == 0 && len(out.ChartNumbers) == 0 &&
		len(out.Images) == 0 && len(out.Products) == 0 && len(out.Logos) == 0 &&
		len(out.Maps) == 0 && len(out.BrandTexts) == 0 && len(out.Quotes) == 0 &&
		len(out.Keywords) == 0 && len(out.MultiEntityGroups) == 0 {
		return nil
	}
	return &out
}

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

func overlayImageLayersHaveCaptions(item capmodel.OverlayItem) bool {
	for _, layer := range item.ImageLayers {
		if strings.TrimSpace(layer.Caption) != "" {
			return true
		}
	}
	return false
}

func overlayGroupOf(item capmodel.OverlayItem) string {
	switch {
	case item.Kind == "map" && item.Map != nil:
		return overlayGroupMaps
	case item.Kind == string(capmodel.KindLocation):
		return overlayGroupLocations
	case item.Kind == "number" || item.Kind == "timeline_date":
		// Number cards share one run-level lane. The planner emits dates and
		// metrics as kind="number" with different templates, so the group must
		// key the lane, not a single template id.
		return overlayGroupDates
	case item.Kind == string(capmodel.KindEntityImage) && (strings.TrimSpace(item.EntityCaption) != "" || overlayImageLayersHaveCaptions(item)):
		return overlayGroupEntitiesWithText
	case entityCardKind(capmodel.OverlayKind(item.Kind)):
		return overlayGroupEntitiesWithText
	case isImageOverlayItem(item):
		return overlayGroupImages
	case item.Params != nil && (item.Params["editorial_role"] == "scene_bullet" || item.Params["editorial_role"] == "chapter_bullet"):
		// Bullets are their own editorial lane: a chapter card is not a long
		// phrase, so it must never spend a long_phrases slot (or be counted as
		// one). Without this case a 10 long-phrase cap silently included the
		// chapter bullets and the requested 10+10+bullets never coexisted.
		return overlayGroupBullets
	case item.Kind == "text_phrase" || item.Kind == string(capmodel.KindImportantPhrase):
		// Phrase lane is a content property. Visible motion IDs are often
		// shared by both lanes, so using recipe membership here misclassifies
		// every short phrase that received a standard entrance.
		if words := len(strings.Fields(item.Text)); words >= 2 && words <= 5 {
			return overlayGroupShortPhrases
		}
		return overlayGroupLongPhrases
	}
	return ""
}

func applyOverlayGroupLimits(items []capmodel.OverlayItem, limits map[string]int) []capmodel.OverlayItem {
	if len(limits) == 0 {
		return items
	}
	// Entity images can carry captions inside the image item and its child
	// layers. Those captions are text overlays too, so an explicit zero for
	// entities_with_text must remove their text and motion before composition.
	if limit, capped := limits[overlayGroupEntitiesWithText]; capped && limit == 0 {
		stripOverlayImageCaptions(items)
	}
	candidates := make(map[string][]int, len(limits))
	for index, item := range items {
		group := overlayGroupOf(item)
		if group == "" {
			continue
		}
		if _, capped := limits[group]; !capped {
			continue
		}
		candidates[group] = append(candidates[group], index)
	}
	keep := make(map[int]struct{}, len(items))
	for group, indices := range candidates {
		seenScenes := make(map[string]struct{}, len(indices))
		diverse := make([]int, 0, len(indices))
		remaining := make([]int, 0, len(indices))
		for _, index := range indices {
			sceneID := strings.TrimSpace(items[index].SceneID)
			if sceneID == "" {
				remaining = append(remaining, index)
				continue
			}
			if _, ok := seenScenes[sceneID]; ok {
				remaining = append(remaining, index)
				continue
			}
			seenScenes[sceneID] = struct{}{}
			diverse = append(diverse, index)
		}
		selected := append(diverse, remaining...)
		if limit := limits[group]; len(selected) > limit {
			selected = selected[:limit]
		}
		for _, index := range selected {
			keep[index] = struct{}{}
		}
	}
	out := items[:0]
	for index, item := range items {
		group := overlayGroupOf(item)
		if _, capped := limits[group]; group == "" || !capped {
			out = append(out, item)
			continue
		}
		if _, ok := keep[index]; ok {
			out = append(out, item)
		}
	}
	return out
}

func stripOverlayImageCaptions(items []capmodel.OverlayItem) {
	for i := range items {
		items[i].EntityCaption = ""
		items[i].CaptionMotionID = ""
		items[i].CaptionMotionParams = nil
		for layer := range items[i].ImageLayers {
			items[i].ImageLayers[layer].Caption = ""
			items[i].ImageLayers[layer].CaptionMotionID = ""
			items[i].ImageLayers[layer].CaptionMotionParams = nil
		}
	}
}

func applyPhraseLaneCaps(config *capabilityoverlay.PlannerConfig, limits map[string]int) (shortCap int, hasShort bool, longCap int, hasLong bool) {
	if config == nil {
		return 0, false, 0, false
	}
	shortCap, hasShort = overlayGroupLimit(limits, overlayGroupShortPhrases)
	longCap, hasLong = overlayGroupLimit(limits, overlayGroupLongPhrases)
	if hasShort && hasLong && shortCap > 0 && longCap > 0 {
		config.RunLevelShortPhraseLimit = shortCap
		config.RunLevelLongPhraseLimit = longCap
	}
	// An explicit bullets cap opens the independent bullet lane. A zero or
	// absent cap keeps the legacy path (chapter cards ride the phrase lane).
	if bulletCap, hasBullets := overlayGroupLimit(limits, overlayGroupBullets); hasBullets && bulletCap > 0 {
		config.RunLevelBulletOverlayLimit = bulletCap
	}
	return shortCap, hasShort, longCap, hasLong
}

func overlayGroupLimit(limits map[string]int, group string) (int, bool) {
	value, ok := limits[group]
	return value, ok
}

func effectivePhraseOverlayCeiling(requested int, limits map[string]int) int {
	ceiling := capabilityoverlay.EffectivePhraseOverlayLimit(requested)
	shortCap, hasShortCap := overlayGroupLimit(limits, overlayGroupShortPhrases)
	longCap, hasLongCap := overlayGroupLimit(limits, overlayGroupLongPhrases)
	if !hasShortCap && !hasLongCap {
		return ceiling
	}
	groupCeiling := 0
	for _, cap := range []int{shortCap, longCap} {
		if cap <= 0 {
			continue
		}
		if groupCeiling >= capabilityoverlay.MaxPhraseOverlaysHardLimit-cap {
			groupCeiling = capabilityoverlay.MaxPhraseOverlaysHardLimit
			break
		}
		groupCeiling += cap
	}
	if groupCeiling > ceiling {
		ceiling = groupCeiling
	}
	return ceiling
}

func effectiveMapOverlayCeiling(requested int, limits map[string]int) int {
	ceiling := 0
	if requested > 0 {
		ceiling = requested
	}
	if group, ok := overlayGroupLimit(limits, overlayGroupMaps); ok && group > 0 && (ceiling == 0 || group < ceiling) {
		ceiling = group
	}
	if ceiling <= 0 || ceiling > capabilityoverlay.MaxMapOverlaysPerRun {
		ceiling = capabilityoverlay.MaxMapOverlaysPerRun
	}
	return ceiling
}
