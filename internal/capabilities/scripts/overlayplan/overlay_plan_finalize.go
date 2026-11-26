// Package scriptgeneration — overlay_plan_finalize.go owns the two
// post-compilation concerns of the production overlay plan: projecting the
// run-level text/image style onto the sealed items, and the per-job Scene
// Intelligence observability report.
//
// Both run AFTER the seal and never rewrite a sealed item: the style projection
// stamps the run-level spec a segment inherited, and the report only observes.
// That ordering is the reason they are a separate responsibility from the
// compile and seal boundary in overlay_plan_compile.go.
//
// Ownership split inside this file:
//
//	ApplyPayloadOverlayStyleToItems → the one run-level OverlayStyleSpec
//	                                  projection (explicit typography, bullet
//	                                  block/pins, image frame/layer params).
//	recap/chart counters      → what survived the overlap + budget gates.
//	SI observability report   → the per-scene "why did this video come out
//	                            this way" snapshot (logic only, no HTTP, no
//	                            persistence; JSON-serializable for a future
//	                            page/log sink).
package overlayplan

import (
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

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

// bulletBaseParamsForScene is the run-level/bullet typography for one bullet,
// minus the fields owned by that bullet's per-scene pin. The pin itself is
// applied by the Rust compiler (the single owner of bullet presentation), so
// re-applying it here would duplicate the decision; overwriting it with the
// run-level block would silently undo it. Fields the pin leaves unset still
// fall back to the run-level block, field by field.
func bulletBaseParamsForScene(sceneID string, pins map[string]scriptpkg.EditorialBulletStyle, base map[string]any) map[string]any {
	params := make(map[string]any, len(base)+2)
	for key, value := range base {
		params[key] = value
	}
	pin, ok := pins[strings.TrimSpace(sceneID)]
	if !ok {
		return params
	}
	if pin.FontFamily != "" {
		delete(params, "font_family")
		deleteNestedStyleKey(params, "font_family")
	}
	if pin.FontSizePX > 0 {
		delete(params, "font_size_px")
		deleteNestedStyleKey(params, "font_size")
	}
	return params
}

// plannerPhraseFamilyLocked reports a plain phrase whose visible family was
// chosen by the planner: short display copy uses Oswald, long editorial copy
// keeps Playfair. The run-level overlay_style must not flatten the two
// families back into one look (ITCLOSE-06).
func plannerPhraseFamilyLocked(params map[string]any) bool {
	if params == nil {
		return false
	}
	family, _ := params["phrase_family"].(string)
	return strings.TrimSpace(family) != ""
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

func applyTextStyleToChainSegment(segment *capmodel.ChainSegment, styleParams map[string]any) {
	if segment == nil || len(styleParams) == 0 {
		return
	}
	segment.Params = capabilityoverlay.MergeRuntimeTextStyleParams(segment.Params, styleParams)
	block := textStyleBlock(styleParams)
	if size, ok := styleParams["glow_size"].(float64); ok && size <= 0 {
		delete(block, "glow")
	}
	if len(block) == 0 {
		return
	}
	segment.Style = capabilityoverlay.MergeStyleParam(segment.Style, block)
}

// overlayItemHasCaption reports a captioned entity/image card. Such a card is
// image-backed, but its visible typography is the caption: it must receive the
// same run-level font/color/glow as a phrase, or the payload's typography stops
// at the portrait and the name silently falls back to the preset.
func overlayItemHasCaption(item capmodel.OverlayItem) bool {
	return strings.TrimSpace(item.EntityCaption) != "" &&
		len(item.AssetRefs) > 0 &&
		capabilityoverlay.IsImageOverlayKind(item.Kind)
}

// chainSegmentHasCaption is the chain-segment mirror of overlayItemHasCaption.
// Segments carry their own kind/caption/assets, so the decision is local to the
// segment rather than inherited from the container.
func chainSegmentHasCaption(segment capmodel.ChainSegment) bool {
	if strings.TrimSpace(segment.EntityCaption) == "" {
		return false
	}
	if !capabilityoverlay.IsImageOverlayKind(segment.Kind) {
		return false
	}
	if strings.TrimSpace(segment.AssetID) != "" {
		return true
	}
	return len(segment.Assets) > 0
}

// ApplyPayloadOverlayStyleToItems is the one payload-style projection onto the
// sealed plan, after the planner's presentation defaults. Non-bullet text uses
// the run-level typography; captioned entity/image cards use it for their
// caption layer too; bullet-point copy (including a bullet fused into a chain)
// uses the dedicated bullet block and falls back to the run-level style field
// by field; image items take the run-level image frame/treatment. Only fields
// the payload actually chose are written, so an omitted field keeps the
// planner's default instead of being reset. Cursor controls are dropped by
// payloadTextStyleParams because the Chronon lowering rejects them.
func ApplyPayloadOverlayStyleToItems(items []capmodel.OverlayItem, canvas OverlayCanvasSpec) {
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
		if segments, ok := item.Segments(); ok {
			changed := false
			for n := range segments {
				segment := &segments[n]
				// Chains carry no item-level asset refs (each photo lives in its
				// own segment), so the image frame and backdrop must be projected
				// onto the segment params. Without this a chain never received
				// image_backdrop, radius or shadow.
				if imageStyle != nil && segmentIsImage(*segment) {
					applyImageStyleToChainSegment(segment, imageStyle)
					changed = true
					continue
				}
				if editorialBulletParams(segment.Params) {
					applyTextStyleToChainSegment(segment, bulletBaseParamsForScene(item.SceneID, pins, bulletStyle))
					changed = true
					continue
				}
				if capabilityoverlay.IsTextOverlayKind(segment.Kind) || chainSegmentHasCaption(*segment) {
					if !plannerPhraseFamilyLocked(segment.Params) {
						applyTextStyleToChainSegment(segment, runStyle)
						changed = true
					}
				}
			}
			if changed {
				item.SetChainSegments(segments)
				item.RenderKey = ""
			}
			continue
		}
		if editorialBulletParams(item.Params) {
			applyTextStyleToItem(item, bulletBaseParamsForScene(item.SceneID, pins, bulletStyle))
			continue
		}
		if capabilityoverlay.IsTextOverlayKind(item.Kind) || overlayItemHasCaption(*item) {
			if !plannerPhraseFamilyLocked(item.Params) {
				applyTextStyleToItem(item, runStyle)
			}
		}
	}
}

// segmentIsImage reports whether a chain segment is an image segment, i.e.
// one that owns an asset and can carry the image treatment.
func segmentIsImage(segment capmodel.ChainSegment) bool {
	return capabilityoverlay.IsImageOverlayKind(segment.Kind)
}

// applyImageStyleToChainSegment projects the run-level image treatment onto
// one image segment: backdrop, radius, shadow, fit and any authored card
// geometry. The segment is lowered as an image item of its own, so the params
// it carries are exactly the ones the renderer reads.
func applyImageStyleToChainSegment(segment *capmodel.ChainSegment, style *scriptpkg.OverlayImageStyleSpec) {
	if segment == nil {
		return
	}
	probe := capmodel.OverlayItem{Kind: "image", Params: segment.Params}
	capabilityoverlay.ApplyImageStyle(&probe, style)
	segment.Params = probe.Params
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
		if segments, ok := items[i].Segments(); ok {
			for _, segment := range segments {
				if segment.Params["recap"] == true {
					n++
				}
			}
		}
	}
	return n
}

// EffectiveVisualMoments is the product-facing density metric: top-level GPU
// render items plus every additional chain segment fused into them. A chain
// with N segments is one GPU render item but N visual moments, so counting
// only Items understates the plan the viewer actually sees.
func EffectiveVisualMoments(items []capmodel.OverlayItem) int {
	total := len(items)
	for i := range items {
		segments, ok := items[i].Segments()
		if !ok || len(segments) <= 1 {
			continue
		}
		total += len(segments) - 1
	}
	return total
}

// countChartItems includes charts preserved inside fused chain items.
func countChartItems(items []capmodel.OverlayItem) int {
	n := 0
	for i := range items {
		if items[i].Params["chart_card"] == true || items[i].Params["chart_bar"] == true {
			n++
		}
		if segments, ok := items[i].Segments(); ok {
			for _, segment := range segments {
				if segment.Params["chart_card"] == true || segment.Params["chart_bar"] == true {
					n++
				}
			}
		}
	}
	return n
}
