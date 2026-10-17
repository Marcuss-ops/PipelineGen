// Package scriptgeneration — overlay_plan_finalize.go owns the two
// post-compilation concerns of the production overlay plan: projecting the
// run-level text/image style onto the sealed items, and the per-job Scene
// Intelligence observability report.
//
// Both were extracted from overlay_plan.go to keep that file inside the
// godlike/08 max_lines_per_file_strict cap (600). The split is a pure move:
// no behaviour changed, the functions are the same ones the sealing path
// called before.
//
// Ownership split inside this file:
//
//	applyOverlayStyleToItems  → the run-level OverlayStyleSpec projection
//	                            (text sizing, image frame/layer params).
//	recap/chart counters      → what survived the overlap + budget gates.
//	SI observability report   → the per-scene "why did this video come out
//	                            this way" snapshot (logic only, no HTTP, no
//	                            persistence; JSON-serializable for a future
//	                            page/log sink).
package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func applyMapPresentationToItems(items []capabilityoverlay.OverlayItem, mapType, cameraAnimation, labelAnimation, mapAnimation string) error {
	mapType = strings.ToLower(strings.TrimSpace(mapType))
	if mapType == "" {
		mapType = "satellite"
	}
	if !capabilityoverlay.AnimationSupportsMapType(mapType) {
		return fmt.Errorf("map_type %q is unsupported; choose roadmap, terrain, satellite, or random", mapType)
	}
	if err := validateRuntimeMapAnimation("map_camera_animation", cameraAnimation, mapCameraAnimationsCertified()); err != nil {
		return err
	}
	if err := validateRuntimeMapAnimation("map_label_animation", labelAnimation, mapLabelAnimationsCertified()); err != nil {
		return err
	}
	if err := validateRuntimeMapAnimation("map_animation", mapAnimation, mapMapAnimationsCertified()); err != nil {
		return err
	}
	for i := range items {
		if items[i].Map == nil {
			continue
		}
		if items[i].Params == nil {
			items[i].Params = map[string]any{}
		}
		items[i].Params["map_type"] = mapType
		if cameraAnimation != "" {
			items[i].Params["map_camera_animation"] = cameraAnimation
		}
		if labelAnimation != "" {
			items[i].Params["map_label_animation"] = labelAnimation
		}
		if mapAnimation != "" {
			items[i].Params["map_animation"] = mapAnimation
		}
	}
	return nil
}

// validateRuntimeMapAnimation fails closed on unknown runtime animation ids.
// Empty and "random" mean "seeded random choice" and always pass; any other
// value must be in the certified list the Python renderer accepts.
func validateRuntimeMapAnimation(field, value string, certified []string) error {
	trimmed := strings.TrimSpace(value)
	if field == "map_label_animation" && trimmed == "none" {
		return nil
	}
	if trimmed == "" || strings.EqualFold(trimmed, "random") {
		return nil
	}
	for _, candidate := range certified {
		if trimmed == candidate {
			return nil
		}
	}
	return fmt.Errorf("%s %q is unsupported; choose random or one of: %s", field, value, strings.Join(certified, ", "))
}

// countRecapItems counts final recap survivors, including cards preserved
// inside a fused chain item.
func countRecapItems(items []capabilityoverlay.OverlayItem) int {
	n := 0
	for i := range items {
		if v, ok := items[i].Params["recap_count"].(int); ok {
			n += v
			continue
		}
		if items[i].Params["recap"] == true {
			n++
		}
		if segments, ok := capabilityoverlay.DecodeChainSegments(items[i].Params); ok {
			for _, segment := range segments {
				if segment.Params["recap"] == true {
					n++
				}
			}
		}
	}
	return n
}

// countChartItems includes charts preserved inside fused chain items.
func countChartItems(items []capabilityoverlay.OverlayItem) int {
	n := 0
	for i := range items {
		if items[i].Params["chart_card"] == true || items[i].Params["chart_bar"] == true {
			n++
		}
		if segments, ok := capabilityoverlay.DecodeChainSegments(items[i].Params); ok {
			for _, segment := range segments {
				if segment.Params["chart_card"] == true || segment.Params["chart_bar"] == true {
					n++
				}
			}
		}
	}
	return n
}

// recapItemsByScene counts the recap cards that survive chain coalescing.
func recapItemsByScene(items []capabilityoverlay.OverlayItem) map[string]int {
	counts := make(map[string]int)
	for i := range items {
		if v, ok := items[i].Params["recap_count"].(int); ok {
			counts[items[i].SceneID] += v
			continue
		}
		if items[i].Params["recap"] == true {
			counts[items[i].SceneID]++
		}
		if segments, ok := capabilityoverlay.DecodeChainSegments(items[i].Params); ok {
			for _, segment := range segments {
				if segment.Params["recap"] == true {
					counts[items[i].SceneID]++
				}
			}
		}
	}
	return counts
}

// applyOverlayStyleToItems projects the run-level OverlayStyleSpec onto every
// item: font size for phrases and presentation cards, the image frame, and the
// per-image-layer params for composite entity images. Extracted verbatim from
// the sealing path so the file-size split changes no behaviour.
func applyOverlayStyleToItems(items []capabilityoverlay.OverlayItem, canvas OverlayCanvasSpec) {
	styleParams := overlayStyleParams(canvas.Style)
	runGlowDisabled := canvas.Style != nil && canvas.Style.GlowSize != nil && *canvas.Style.GlowSize <= 0
	for i := range items {
		merged := map[string]any{}
		for k, v := range items[i].Params {
			merged[k] = v
		}
		for k, v := range styleParams {
			if isRuntimeTextStyleParam(k) && (isImageOverlayItem(items[i]) || (!isTextOverlayKind(items[i].Kind) && strings.TrimSpace(items[i].EntityCaption) == "" && items[i].Map == nil)) {
				continue
			}
			if strings.HasPrefix(k, "image_") {
				if !isImageOverlayItem(items[i]) {
					continue
				}
				key := strings.TrimPrefix(k, "image_")
				merged[key] = v
				continue
			}
			if k == "style" {
				merged[k] = mergeStyleParam(merged[k], v.(map[string]any))
				// Motion presets also carry a top-level Style map. Keep explicit
				// run typography/color/effect choices in sync there; chain
				// coalescing copies this map into each ChainSegment, and the
				// renderer gives it precedence over Params.style.
				if isTextOverlayKind(items[i].Kind) || strings.TrimSpace(items[i].EntityCaption) != "" || items[i].Map != nil {
					items[i].Style = mergeStyleParam(items[i].Style, v.(map[string]any))
				}
			} else if isRuntimeTextStyleParam(k) {
				// A run-level payload choice is canonical across text motions.
				// Preset or animation-derived values are defaults and must not
				// make the same payload render with different typography.
				merged[k] = v
			} else if _, exists := merged[k]; !exists {
				merged[k] = v
			}
		}
		// A run-level glow of zero means "no glow". StyleParams omits disabled
		// glows (an explicit zero renders black frames in Chronon), so the
		// animation's own glow must be removed here or it would survive the run choice.
		if runGlowDisabled && (isTextOverlayKind(items[i].Kind) || strings.TrimSpace(items[i].EntityCaption) != "") && items[i].Map == nil && !isImageOverlayItem(items[i]) {
			delete(merged, "glow_size")
			delete(merged, "glow_color")
			if nested, ok := merged["style"].(map[string]any); ok {
				delete(nested, "glow")
			}
			if items[i].Style != nil {
				delete(items[i].Style, "glow")
			}
		}
		items[i].Params = merged
		if items[i].Kind == "text_phrase" || items[i].Kind == string(capabilityoverlay.KindImportantPhrase) {
			phraseFontSize := float64(capabilityoverlay.PresentationPhraseFontMinimumPX)
			if configured, ok := styleParams["font_size_px"].(float64); ok && configured > phraseFontSize {
				phraseFontSize = configured
			}
			items[i].Params["font_size_px"] = phraseFontSize
			if phraseStyle, ok := items[i].Params["style"].(map[string]any); ok {
				phraseStyle["font_size"] = phraseFontSize
			}
		}
		if items[i].TemplateID == "TIMELINE_DATE_CARD" || items[i].TemplateID == "METRIC_STAT_CARD" {
			// Match the selected shared text style and apply the presentation
			// size increase without adding a separate preset or font family.
			baseFontSize := float64(capabilityoverlay.SharedTextFontSizePX)
			if configured, ok := styleParams["font_size_px"].(float64); ok && configured > baseFontSize {
				baseFontSize = configured
			}
			presentationFontSize := baseFontSize + float64(capabilityoverlay.PresentationTextFontIncreasePX)
			items[i].Params["font_size_px"] = presentationFontSize
			if presentationStyle, ok := items[i].Params["style"].(map[string]any); ok {
				presentationStyle["font_size"] = presentationFontSize
			}
			// Kind "number" is semantically lowered by RenderingGen through
			// its generic text lane, so it misses the date/stat box growth
			// branch. Send the matching height with the font override or the
			// preset's shrink-only fit silently scales 240px callouts back down.
			items[i].Params["height"] = presentationFontSize*1.7 + 32
		}
		items[i].RenderKey = ""
		if isImageOverlayItem(items[i]) {
			var imageStyle *scriptpkg.OverlayImageStyleSpec
			if canvas.Style != nil {
				imageStyle = canvas.Style.Image
			}
			applyOverlayImageStyle(&items[i], imageStyle)
			for childIndex := range items[i].ImageLayers {
				child := &items[i].ImageLayers[childIndex]
				childParams := map[string]any{}
				for key, value := range child.Params {
					childParams[key] = value
				}
				for key, value := range styleParams {
					if strings.HasPrefix(key, "image_") {
						name := strings.TrimPrefix(key, "image_")
						childParams[name] = value
					}
				}
				child.Params = childParams
			}
			normalizeEntityImageLayer(&items[i])
		}
	}
}
