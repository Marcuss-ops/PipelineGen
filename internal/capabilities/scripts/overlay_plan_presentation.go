package scriptgeneration

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

const (
	maxPhraseOverlayRunes = 180
	minPhraseOverlayRunes = 80
	phraseSplitFontSize   = 64.0
	phraseDefaultFontSize = 72.0
	// playfairRuntimeFamily is the only Playfair cut staged by the renderer.
	// The presentation layer must never emit a display name or a hypothetical
	// family that runtimeFontAssets cannot resolve.
	playfairRuntimeFamily = "playfair_display_italic"
	// The requested documentary treatment is solid black type with a light
	// white halo. A black drop shadow disappeared into dark footage and could
	// not separate the glyph from the picture, so it is replaced by the glow.
	presentationTextFill      = "#000000"
	presentationTextGlowColor = "#FFFFFF"
	presentationTextGlowPX    = 10.0
)

// applyOverlayPresentation is the structural policy applied to every generated
// plan before it is sealed. It does not pin one style or one animation: entity
// cards keep the planner's random selector and the planner's random motion
// pools. It only removes invalid surfaces (unverified images, side captions),
// applies the requested type treatment, and splits text that would otherwise
// be clipped by the renderer.
func applyOverlayPresentation(items []capmodel.OverlayItem, planID string) []capmodel.OverlayItem {
	if len(items) == 0 {
		return items
	}
	items = applyVerifiedEntityAssets(items, planID)
	items = splitLongOverlayText(items, planID)
	for index := range items {
		normalizePresentationFonts(&items[index])
		applyPresentationTypography(&items[index])
	}
	return items
}

// splitLongOverlayText turns a long phrase or a text-only chain into several
// independently timed phrases. The original motion is retained on every chunk
// when the item already had one; otherwise a motion is selected from the same
// random phrase pool the planner uses. No global "one animation" rule exists.
func splitLongOverlayText(items []capmodel.OverlayItem, planID string) []capmodel.OverlayItem {
	out := make([]capmodel.OverlayItem, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		if utf8.RuneCountInString(text) <= maxPhraseOverlayRunes {
			out = append(out, item)
			continue
		}
		if !longTextItemSplittable(item) {
			out = append(out, item)
			continue
		}
		chunks := splitPhraseText(text, maxPhraseOverlayRunes)
		if len(chunks) <= 1 {
			out = append(out, item)
			continue
		}
		out = append(out, phraseChunks(item, chunks, planID)...)
	}
	return out
}

// longTextItemSplittable refuses to destroy a chain that actually contains an
// image. Long text-only chains are exactly the case that used to reach the
// renderer as one oversized, clipped text layer.
func longTextItemSplittable(item capmodel.OverlayItem) bool {
	switch strings.TrimSpace(item.Kind) {
	case "text_phrase", "important_phrase", "phrase_chain", "mixed_chain":
		return len(item.AssetRefs) == 0 && len(item.ImageLayers) == 0 && !chainHasVisualSegment(item)
	default:
		return false
	}
}

func chainHasVisualSegment(item capmodel.OverlayItem) bool {
	for _, segment := range chainSegments(item) {
		kind, _ := segment["kind"].(string)
		if strings.Contains(strings.ToLower(kind), "image") {
			return true
		}
		if assetID, _ := segment["asset_id"].(string); strings.TrimSpace(assetID) != "" {
			return true
		}
		if assets, ok := segment["assets"].([]any); ok && len(assets) > 0 {
			return true
		}
	}
	return false
}

func splitPhraseText(text string, maxRunes int) []string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return []string{text}
	}
	out := make([]string, 0, len(runes)/maxRunes+1)
	for len(runes) > 0 {
		if len(runes) <= maxRunes {
			out = append(out, strings.TrimSpace(string(runes)))
			break
		}
		cut := maxRunes
		for i := maxRunes; i > minPhraseOverlayRunes; i-- {
			switch runes[i-1] {
			case '.', '!', '?', ';', ':':
				cut = i
			}
			if cut != maxRunes {
				break
			}
		}
		if cut == maxRunes {
			for i := maxRunes; i > minPhraseOverlayRunes; i-- {
				if runes[i-1] == ' ' {
					cut = i
					break
				}
			}
		}
		chunk := strings.TrimSpace(string(runes[:cut]))
		if chunk != "" {
			out = append(out, chunk)
		}
		runes = runes[cut:]
		for len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	if len(out) == 0 {
		return []string{text}
	}
	return out
}

func phraseChunks(base capmodel.OverlayItem, chunks []string, planID string) []capmodel.OverlayItem {
	start, duration := overlayItemWindow(base)
	if duration <= 0 {
		duration = 3_000_000
	}
	totalRunes := 0
	for _, chunk := range chunks {
		totalRunes += utf8.RuneCountInString(chunk)
	}
	if totalRunes == 0 {
		return []capmodel.OverlayItem{base}
	}
	out := make([]capmodel.OverlayItem, 0, len(chunks))
	cursor := start
	for index, chunk := range chunks {
		chunkDuration := int64(0)
		if index == len(chunks)-1 {
			chunkDuration = start + duration - cursor
		} else {
			chunkDuration = duration * int64(utf8.RuneCountInString(chunk)) / int64(totalRunes)
		}
		if chunkDuration < 1_200_000 {
			chunkDuration = 1_200_000
		}
		if cursor+chunkDuration > start+duration {
			chunkDuration = start + duration - cursor
		}
		if chunkDuration <= 0 {
			continue
		}
		item := base
		item.ID = base.ID + "-part-" + string(rune('a'+index))
		item.Kind = "text_phrase"
		item.TemplateID = "IMPORTANT_PHRASE"
		item.PresetID = "phrase_default"
		item.ImagePresetID = ""
		item.EntityCaption = ""
		item.CaptionMotionID = ""
		item.CaptionFontFamily = ""
		item.CaptionLayout = ""
		item.CaptionColor = ""
		item.AssetRefs = nil
		item.ImageLayers = nil
		item.GroupMotionID = ""
		item.Frame = nil
		item.Map = nil
		item.EntityRef = nil
		item.EntityID = ""
		item.Params = nil
		item.Style = nil
		item.RenderKey = ""
		item.Text = chunk
		item.StartUS = cursor
		item.DurationUS = chunkDuration
		item.StartMs = cursor / 1000
		item.EndMs = (cursor + chunkDuration + 999) / 1000
		if strings.TrimSpace(item.MotionID) == "" {
			item.MotionID = capabilityoverlay.SelectTextMotion(planID, item.SceneID, item.ID)
		}
		out = append(out, item)
		cursor += chunkDuration
	}
	if len(out) == 0 {
		return []capmodel.OverlayItem{base}
	}
	return out
}

func overlayItemWindow(item capmodel.OverlayItem) (int64, int64) {
	if item.DurationUS > 0 {
		return item.StartUSValue(), item.DurationUS
	}
	if item.EndMs > item.StartMs {
		return item.StartMs * 1000, (item.EndMs - item.StartMs) * 1000
	}
	return 0, 0
}

func applyPresentationTypography(item *capmodel.OverlayItem) {
	switch strings.TrimSpace(item.Kind) {
	case "entity_image":
		applyEntityCaptionTypography(item)
	case "image_chain", "mixed_chain", "phrase_chain":
		applyChainCaptionTypography(item)
	case "text_phrase", "important_phrase":
		applyPhraseTypography(item)
	case "location", "organization", "concept":
		applyTextTypography(item, 96, 1200, 320)
	case "number":
		applyTextTypography(item, 140, 900, 260)
	case "quote":
		applyTextTypography(item, 72, 1500, 700)
	}
}

func applyEntityCaptionTypography(item *capmodel.OverlayItem) {
	item.CaptionLayout = "below"
	if strings.TrimSpace(item.EntityCaption) == "" && !imageLayersHaveCaption(item.ImageLayers) {
		// A media layer without text cannot carry text runtime overrides.
		// Keep its image style untouched and fail open as a pure image.
		item.CaptionFontFamily = ""
		item.CaptionColor = ""
		stripPresentationTextKeys(item.Params)
		stripPresentationTextKeys(item.Style)
		item.RenderKey = ""
		return
	}
	// Pin the below-caption pool: the generic random selector can still
	// resolve a layout whose name sits in a side column.
	item.EntityStyleID = capabilityoverlay.IdentityEntityStyleBelowSelector
	item.CaptionFontFamily = playfairRuntimeFamily
	if isSideCaptionMotion(item.CaptionMotionID) {
		// Empty lets the below-caption style pick from its random pool; do
		// not pin one replacement motion.
		item.CaptionMotionID = ""
	}
	item.CaptionColor = presentationTextFill
	item.Params = presentationParams(item.Params)
	item.Params["caption_layout"] = "below"
	item.Params["position_x"] = 0.0
	item.Style = presentationStyle(item.Style, 0)
	item.RenderKey = ""
}

func imageLayersHaveCaption(layers []capmodel.OverlayImageLayer) bool {
	for _, layer := range layers {
		if strings.TrimSpace(layer.Caption) != "" {
			return true
		}
	}
	return false
}

// applyChainCaptionTypography treats the chain container as geometry, never as
// a text layer. Text runtime keys on an image-chain container are rejected by
// the renderer, so typography is applied per text segment and captions stay on
// caption fields only.
func applyChainCaptionTypography(item *capmodel.OverlayItem) {
	item.CaptionLayout = "below"
	item.CaptionFontFamily = playfairRuntimeFamily
	if isSideCaptionMotion(item.CaptionMotionID) {
		item.CaptionMotionID = ""
	}
	item.Params = presentationChainParams(item.Params)
	stripPresentationTextKeys(item.Style)
	// BuildPlan holds chain segments as []ChainSegment in memory; a map-only
	// loop silently skipped them and left image captions white. Decode both
	// the typed and the durable wire form, normalize every segment, then put
	// back maps because later readers (and fingerprinting) expect the wire
	// contract.
	segments, ok := capmodel.DecodeChainSegments(item.Params)
	if !ok {
		item.RenderKey = ""
		return
	}
	for index := range segments {
		segment := &segments[index]
		if chainSegmentIsText(*segment) {
			segment.Params = presentationParams(segment.Params)
			segment.Style = presentationStyle(segment.Style, 0)
		} else {
			// Image, map and shape segments own image-only params; only the
			// caption style may carry the black/white typography, never a
			// runtime font on the image layer itself.
			stripPresentationTextKeys(segment.Params)
			if strings.TrimSpace(segment.EntityCaption) != "" {
				segment.Style = presentationStyle(segment.Style, 0)
			} else {
				stripPresentationTextKeys(segment.Style)
			}
		}
		if strings.TrimSpace(segment.EntityCaption) == "" {
			continue
		}
		segment.CaptionLayout = "below"
		segment.CaptionFontFamily = playfairRuntimeFamily
		segment.CaptionColor = presentationTextFill
		segment.EntityStyleID = capabilityoverlay.IdentityEntityStyleBelowSelector
		if isSideCaptionMotion(segment.CaptionMotionID) {
			// Do not invent a replacement motion here: an empty override lets
			// the below-caption style choose from the renderer's random pool.
			segment.CaptionMotionID = ""
		}
	}
	if data, err := json.Marshal(segments); err == nil {
		var wire []any
		if json.Unmarshal(data, &wire) == nil {
			item.Params["chain_segments"] = wire
		}
	}
	item.RenderKey = ""
}

func chainSegmentIsText(segment capmodel.ChainSegment) bool {
	kind := strings.ToLower(strings.TrimSpace(segment.Kind))
	if strings.Contains(kind, "image") || strings.Contains(kind, "video") || strings.Contains(kind, "map") || strings.Contains(kind, "shape") {
		return false
	}
	if strings.TrimSpace(segment.Text) != "" {
		return true
	}
	template := strings.ToLower(strings.TrimSpace(segment.TemplateID))
	return strings.Contains(kind, "text") || strings.Contains(kind, "phrase") ||
		strings.Contains(kind, "number") || strings.Contains(kind, "quote") ||
		strings.Contains(template, "phrase")
}

func presentationChainParams(base map[string]any) map[string]any {
	if base == nil {
		return nil
	}
	out := make(map[string]any, len(base))
	for key, value := range base {
		if key == "background" {
			continue
		}
		out[key] = value
	}
	stripPresentationTextKeys(out)
	return out
}

// presentationTextStyleKeys mirrors RenderingGen's runtimeTextStyleKeys. They
// are text-layer controls; carrying one on an image, video, map or chain
// container is a renderer error, not a cosmetic difference.
var presentationTextStyleKeys = []string{
	"font_family", "font_size_px", "fill", "glow_size", "glow_color", "stroke_size",
	"shadow_blur_px", "shadow_opacity", "shadow_offset_x_px", "shadow_offset_y_px",
}

func stripPresentationTextKeys(values map[string]any) {
	if values == nil {
		return
	}
	for _, key := range presentationTextStyleKeys {
		delete(values, key)
	}
}

func applyPhraseTypography(item *capmodel.OverlayItem) {
	fontSize := phraseDefaultFontSize
	if existing, ok := numericParam(item.Params, "font_size_px"); ok && existing > 0 {
		fontSize = existing
	}
	runes := utf8.RuneCountInString(strings.TrimSpace(item.Text))
	if runes > 120 && fontSize > phraseDefaultFontSize {
		fontSize = phraseDefaultFontSize
	}
	if runes > 160 {
		fontSize = phraseSplitFontSize
	}
	applyTextTypography(item, fontSize, 1700, 600)
}

func isSideCaptionMotion(motion string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(motion)), "split_side")
}

// normalizePresentationFonts closes the runtime-font boundary before any
// renderer sees the plan. It walks nested chain segments and image layers too,
// because runtimeFontAssets stages every explicit font_family it finds there.
// A single stale alias must not turn a render into a late worker failure.
func normalizePresentationFonts(item *capmodel.OverlayItem) {
	normalizeFontMap(item.Params)
	normalizeFontMap(item.Style)
	if strings.TrimSpace(item.CaptionFontFamily) != "" {
		item.CaptionFontFamily = playfairRuntimeFamily
	}
	for index := range item.ImageLayers {
		layer := &item.ImageLayers[index]
		normalizeFontMap(layer.Params)
		if strings.TrimSpace(layer.CaptionFontFamily) != "" {
			layer.CaptionFontFamily = playfairRuntimeFamily
		}
		layer.CaptionLayout = "below"
		if strings.TrimSpace(layer.Caption) != "" {
			layer.CaptionColor = presentationTextFill
		}
		if isSideCaptionMotion(layer.CaptionMotionID) {
			layer.CaptionMotionID = ""
		}
	}
}

func normalizeFontMap(values map[string]any) {
	if values == nil {
		return
	}
	for key, value := range values {
		switch key {
		case "font_family", "caption_font_family":
			if family, ok := value.(string); ok && strings.TrimSpace(family) != "" {
				values[key] = playfairRuntimeFamily
			}
		case "caption_layout":
			values[key] = "below"
		case "caption_motion_id":
			if motion, ok := value.(string); ok && isSideCaptionMotion(motion) {
				values[key] = ""
			}
		default:
			normalizeFontValue(value)
		}
	}
}

func normalizeFontValue(value any) {
	switch nested := value.(type) {
	case map[string]any:
		normalizeFontMap(nested)
	case []any:
		for _, child := range nested {
			normalizeFontValue(child)
		}
	case []map[string]any:
		for _, child := range nested {
			normalizeFontMap(child)
		}
	}
}

func numericParam(params map[string]any, key string) (float64, bool) {
	if params == nil {
		return 0, false
	}
	switch value := params[key].(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	default:
		return 0, false
	}
}

func applyTextTypography(item *capmodel.OverlayItem, fontSize float64, width, height float64) {
	item.Params = presentationParams(item.Params)
	item.Params["font_size_px"] = fontSize
	item.Params["width"] = width
	item.Params["height"] = height
	// Text-only entity cards inherited the preset's default horizontal slot;
	// centering them keeps every entity label on the same axis.
	item.Params["position"] = "center"
	item.Params["position_x"] = 0.0
	item.Style = presentationStyle(item.Style, fontSize)
	item.RenderKey = ""
}

func presentationParams(base map[string]any) map[string]any {
	params := map[string]any{}
	for key, value := range base {
		if key == "background" {
			continue
		}
		params[key] = value
	}
	params["font_family"] = playfairFamily(params["font_family"])
	params["fill"] = presentationTextFill
	params["glow_size"] = presentationTextGlowPX
	params["glow_color"] = presentationTextGlowColor
	for _, key := range []string{"shadow_color", "shadow_blur_px", "shadow_opacity", "shadow_offset_x_px", "shadow_offset_y_px"} {
		delete(params, key)
	}
	if nested, ok := params["style"].(map[string]any); ok {
		copy := map[string]any{}
		for key, value := range nested {
			if key == "background" || key == "shadow" {
				continue
			}
			copy[key] = value
		}
		copy["fill"] = presentationTextFill
		copy["glow"] = map[string]any{"color": presentationTextGlowColor}
		params["style"] = copy
	}
	return params
}

// playfairFamily resolves every requested family to the one Playfair cut the
// renderer actually stages. Accepting an authored spelling such as
// "playfair_display" here would defer the failure to the render worker, where
// the plan can no longer be repaired.
func playfairFamily(_ any) string {
	return playfairRuntimeFamily
}

func presentationStyle(base map[string]any, fontSize float64) map[string]any {
	style := map[string]any{}
	for key, value := range base {
		if key == "background" || key == "shadow" {
			continue
		}
		style[key] = value
	}
	style["font_family"] = playfairFamily(style["font_family"])
	style["fill"] = presentationTextFill
	style["glow"] = map[string]any{"color": presentationTextGlowColor}
	if fontSize > 0 {
		style["font_size"] = fontSize
	}
	return style
}
