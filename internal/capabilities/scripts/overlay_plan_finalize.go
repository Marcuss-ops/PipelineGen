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
//	applyPayloadOverlayStyleToItems → the one run-level OverlayStyleSpec
//	                                  projection (explicit typography, bullet
//	                                  block/pins, image frame/layer params).
//	recap/chart counters      → what survived the overlap + budget gates.
//	SI observability report   → the per-scene "why did this video come out
//	                            this way" snapshot (logic only, no HTTP, no
//	                            persistence; JSON-serializable for a future
//	                            page/log sink).
package scriptgeneration

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// payloadTextStyleParams projects the run-level typography the payload
// explicitly selected. Cursor controls are dropped here: the Chronon text
// lowering rejects them, so projecting an accepted-but-unrenderable control
// would fail a run the schema lets through. An empty map means the payload
// chose no text typography and the planner's presentation defaults stand.
func payloadTextStyleParams(style *scriptpkg.OverlayStyleSpec) map[string]any {
	if style == nil {
		return nil
	}
	params := capabilityoverlay.StyleParams(style)
	for key := range params {
		if key == "style" {
			continue
		}
		if !capabilityoverlay.IsRuntimeTextStyleParam(key) || strings.HasPrefix(key, "cursor_") {
			delete(params, key)
		}
	}
	return params
}

// payloadBulletStyleParams layers the dedicated bullet block over the
// run-level typography. A field the block leaves unset keeps the run-level
// value, which is what makes the second font optional: absent means "use the
// font the payload already chose".
func payloadBulletStyleParams(style *scriptpkg.OverlayStyleSpec) map[string]any {
	base := payloadTextStyleParams(style)
	params := make(map[string]any, len(base)+4)
	for key, value := range base {
		params[key] = value
	}
	if style == nil || style.Bullet == nil {
		return params
	}
	bullet := style.Bullet
	if bullet.FontFamily != "" {
		params["font_family"] = bullet.FontFamily
	}
	if bullet.FontSizePX != nil {
		params["font_size_px"] = *bullet.FontSizePX
		params["style"] = capabilityoverlay.MergeStyleParam(params["style"], map[string]any{"font_size": *bullet.FontSizePX})
	}
	if bullet.GlowSize != nil {
		params["glow_size"] = *bullet.GlowSize
		if *bullet.GlowSize <= 0 {
			deleteNestedStyleKey(params, "glow")
		}
	}
	if bullet.GlowColor != "" {
		params["glow_color"] = bullet.GlowColor
		if size, _ := params["glow_size"].(float64); size > 0 {
			params["style"] = capabilityoverlay.MergeStyleParam(params["style"], map[string]any{
				"glow": map[string]any{"color": bullet.GlowColor},
			})
		}
	}
	if bullet.StrokeSize != nil {
		params["stroke_size"] = *bullet.StrokeSize
	}
	return params
}

// editorialBulletPins indexes the per-scene pins by their trimmed scene id.
func editorialBulletPins(styles []scriptpkg.EditorialBulletStyle) map[string]scriptpkg.EditorialBulletStyle {
	if len(styles) == 0 {
		return nil
	}
	pins := make(map[string]scriptpkg.EditorialBulletStyle, len(styles))
	for _, style := range styles {
		if scene := strings.TrimSpace(style.SceneID); scene != "" {
			pins[scene] = style
		}
	}
	return pins
}

// bulletParamsForScene is the effective bullet typography of one bullet: the
// run-level/bullet selection with that bullet's own pin applied last. The pin
// names one bullet, so it is more specific than the run-level block and wins.
func bulletParamsForScene(sceneID string, pins map[string]scriptpkg.EditorialBulletStyle, base map[string]any) map[string]any {
	params := make(map[string]any, len(base)+2)
	for key, value := range base {
		params[key] = value
	}
	pin, ok := pins[strings.TrimSpace(sceneID)]
	if !ok {
		return params
	}
	if pin.FontFamily != "" {
		params["font_family"] = pin.FontFamily
		params["style"] = capabilityoverlay.MergeStyleParam(params["style"], map[string]any{"font_family": pin.FontFamily})
	}
	if pin.FontSizePX > 0 {
		params["font_size_px"] = pin.FontSizePX
		params["style"] = capabilityoverlay.MergeStyleParam(params["style"], map[string]any{"font_size": pin.FontSizePX})
	}
	return params
}

// editorialBulletParams reports whether an item/segment is bullet-point copy.
// Chapter bullets ride the same lane and share the look.
func editorialBulletParams(params map[string]any) bool {
	if params == nil {
		return false
	}
	role, _ := params["editorial_role"].(string)
	return role == "scene_bullet" || role == capabilityoverlay.ChapterBulletRole
}

// textStyleBlock is the item.Style mirror of the effective typography.
// RenderingGen merges Style over Params, so a family only written flat would
// be shadowed by the presentation pass's Style entry.
func textStyleBlock(styleParams map[string]any) map[string]any {
	block := map[string]any{}
	if family, ok := styleParams["font_family"].(string); ok && family != "" {
		block["font_family"] = family
	}
	if size, ok := styleParams["font_size_px"].(float64); ok {
		block["font_size"] = size
	}
	if nested, ok := styleParams["style"].(map[string]any); ok {
		for key, value := range nested {
			block[key] = value
		}
	}
	return block
}

func deleteNestedStyleKey(params map[string]any, key string) {
	nested, ok := params["style"].(map[string]any)
	if !ok {
		return
	}
	delete(nested, key)
}

func applyTextStyleToItem(item *capmodel.OverlayItem, styleParams map[string]any) {
	if item == nil || len(styleParams) == 0 {
		return
	}
	item.Params = capabilityoverlay.MergeRuntimeTextStyleParams(item.Params, styleParams)
	block := textStyleBlock(styleParams)
	if size, ok := styleParams["glow_size"].(float64); ok && size <= 0 {
		delete(block, "glow")
	}
	if len(block) > 0 {
		item.Style = capabilityoverlay.MergeStyleParam(item.Style, block)
	}
	item.RenderKey = ""
}

func applyTextStyleToChainSegment(segment map[string]any, styleParams map[string]any) {
	if len(styleParams) == 0 {
		return
	}
	params, _ := segment["params"].(map[string]any)
	segment["params"] = capabilityoverlay.MergeRuntimeTextStyleParams(params, styleParams)
	block := textStyleBlock(styleParams)
	if size, ok := styleParams["glow_size"].(float64); ok && size <= 0 {
		delete(block, "glow")
	}
	if len(block) == 0 {
		return
	}
	style, _ := segment["style"].(map[string]any)
	segment["style"] = capabilityoverlay.MergeStyleParam(style, block)
}

// applyPayloadOverlayStyleToItems is the one payload-style projection onto the
// sealed plan, after the planner's presentation defaults. Non-bullet text uses
// the run-level typography; bullet-point copy (including a bullet fused into a
// chain) uses the dedicated bullet block and falls back to the run-level style
// field by field; image items take the run-level image frame/treatment. Only
// fields the payload actually chose are written, so an omitted field keeps the
// planner's default instead of being reset. Cursor controls are dropped by
// payloadTextStyleParams because the Chronon lowering rejects them.
func applyPayloadOverlayStyleToItems(items []capmodel.OverlayItem, canvas OverlayCanvasSpec) {
	runStyle := payloadTextStyleParams(canvas.Style)
	bulletStyle := payloadBulletStyleParams(canvas.Style)
	pins := editorialBulletPins(canvas.EditorialBulletStyles)
	var imageStyle *scriptpkg.OverlayImageStyleSpec
	if canvas.Style != nil {
		imageStyle = canvas.Style.Image
	}
	if len(runStyle) == 0 && len(bulletStyle) == 0 && len(pins) == 0 && imageStyle == nil {
		return
	}
	for i := range items {
		item := &items[i]
		if item.Map != nil {
			continue
		}
		if imageStyle != nil && isImageOverlayItem(*item) {
			applyOverlayImageStyle(item, imageStyle)
		}
		if segments, ok := capmodel.ChainSegmentMaps(item.Params); ok {
			changed := false
			for _, segment := range segments {
				segmentParams, _ := segment["params"].(map[string]any)
				if editorialBulletParams(segmentParams) {
					applyTextStyleToChainSegment(segment, bulletParamsForScene(item.SceneID, pins, bulletStyle))
					changed = true
					continue
				}
				kind, _ := segment["kind"].(string)
				if capabilityoverlay.IsTextOverlayKind(kind) {
					applyTextStyleToChainSegment(segment, runStyle)
					changed = true
				}
			}
			if changed {
				item.Params["chain_segments"] = segments
				item.RenderKey = ""
			}
			continue
		}
		if editorialBulletParams(item.Params) {
			applyTextStyleToItem(item, bulletParamsForScene(item.SceneID, pins, bulletStyle))
			continue
		}
		if capabilityoverlay.IsTextOverlayKind(item.Kind) {
			applyTextStyleToItem(item, runStyle)
		}
	}
}

// countRecapItems counts final recap survivors, including cards preserved
// inside a fused chain item.
func countRecapItems(items []capmodel.OverlayItem) int {
	n := 0
	for i := range items {
		if v, ok := items[i].Params["recap_count"].(int); ok {
			n += v
			continue
		}
		if items[i].Params["recap"] == true {
			n++
		}
		if segments, ok := capmodel.DecodeChainSegments(items[i].Params); ok {
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
func countChartItems(items []capmodel.OverlayItem) int {
	n := 0
	for i := range items {
		if items[i].Params["chart_card"] == true || items[i].Params["chart_bar"] == true {
			n++
		}
		if segments, ok := capmodel.DecodeChainSegments(items[i].Params); ok {
			for _, segment := range segments {
				if segment.Params["chart_card"] == true || segment.Params["chart_bar"] == true {
					n++
				}
			}
		}
	}
	return n
}
