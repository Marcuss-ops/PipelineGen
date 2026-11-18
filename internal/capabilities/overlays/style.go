// Package overlays owns overlay-specific policy used by the scripts capability.
package overlays

import (
	"fmt"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// maxImageFrameShadowBlurPX is the strict-Vulkan separable-blur ceiling the
// native image-card path certifies (Chronon's native drop shadow accepts
// radius <= 64). The same bound gates the payload at ingress, and the
// projection passes the authored value through: ImageFrame and the flat
// runtime override (consumed by frameFromImageParams) never clamp it, so the
// declared look and the rendered look cannot diverge silently.
const maxImageFrameShadowBlurPX = scriptpkg.MaxImageShadowBlurPX

// BackgroundFromPayload projects a kernel background into the render plan
// domain while retaining verified asset references and style fields.
func BackgroundFromPayload(src *scriptpkg.OverlayBackgroundSpec) *capmodel.OverlayBackground {
	if src == nil {
		return nil
	}
	background := &capmodel.OverlayBackground{
		Kind: src.Kind, Color: append([]float64(nil), src.Color...), Fit: src.Fit,
		Opacity: src.Opacity, Loop: src.Loop,
	}
	if params := StyleParams(src.Style); params != nil {
		if style, ok := params["style"].(map[string]any); ok {
			background.Style = style
		}
	}
	if src.AssetID != "" || src.URL != "" || src.SHA256 != "" {
		background.AssetRefs = []capmodel.OverlayAssetRef{{
			AssetID: src.AssetID, URL: src.URL, LocalPath: src.LocalPath,
			SHA256: src.SHA256, MediaType: src.MediaType,
		}}
	}
	return background
}

// StyleParams projects the script kernel's runtime style specification to
// renderer parameters. Returned nested maps and slices are owned by the result.
func StyleParams(style *scriptpkg.OverlayStyleSpec) map[string]any {
	if style == nil {
		return nil
	}
	params := map[string]any{}
	if len(style.Color) > 0 {
		fill := rgbaHex(style.Color)
		params["color"] = append([]float64(nil), style.Color...)
		params["fill"] = fill
		params["style"] = map[string]any{"fill": fill}
	}
	if style.Size != nil {
		if style.Size.Width != nil {
			params["box_width"] = *style.Size.Width
			params["width"] = *style.Size.Width
		}
		if style.Size.Height != nil {
			params["box_height"] = *style.Size.Height
			params["height"] = *style.Size.Height
		}
		if style.Size.FontSize != nil {
			params["style"] = mergeStyleParam(params["style"], map[string]any{"font_size": *style.Size.FontSize})
			params["font_size_px"] = *style.Size.FontSize
		}
	}
	if style.FontFamily != "" {
		params["font_family"] = style.FontFamily
	}
	if background := style.CardBackground; background != nil {
		plate := map[string]any{"color": background.Color}
		if background.Opacity != nil {
			plate["opacity"] = *background.Opacity
		}
		if background.Radius != nil {
			plate["radius"] = *background.Radius
		}
		if len(background.Padding) > 0 {
			plate["padding"] = append([]float64(nil), background.Padding...)
		}
		params["style"] = mergeStyleParam(params["style"], map[string]any{"background": plate})
	}
	// Explicit zero disables the effect. The renderer consumes the zero in
	// applyTextRuntimeOverrides and clears the preset/role effect there; omitting
	// the key instead would let the phrase preset's glow (42) and stroke (3.5)
	// survive a payload that asked for neither. GlowColor is independent of the
	// radius: a color-only override recolors an existing motion/preset glow.
	if style.GlowSize != nil {
		params["glow_size"] = *style.GlowSize
	}
	if style.GlowColor != "" {
		params["glow_color"] = style.GlowColor
		if style.GlowSize != nil && *style.GlowSize > 0 {
			params["style"] = mergeStyleParam(params["style"], map[string]any{
				"glow": map[string]any{"color": style.GlowColor},
			})
		}
	}
	if style.StrokeSize != nil {
		params["stroke_size"] = *style.StrokeSize
	}
	if cursor := style.Cursor; cursor != nil {
		if cursor.Color != "" {
			params["cursor_color"] = cursor.Color
		}
		if cursor.Width != nil {
			params["cursor_width_px"] = *cursor.Width
		}
		if cursor.Height != nil {
			params["cursor_height_px"] = *cursor.Height
		}
		if cursor.Blink != nil {
			params["cursor_blink"] = *cursor.Blink
		}
	}
	if style.TransitionIn != nil && strings.TrimSpace(style.TransitionIn.Preset) != "" {
		animation := map[string]any{"preset": style.TransitionIn.Preset}
		if style.TransitionIn.DurationFrames > 0 {
			animation["enter"] = map[string]any{"duration_frames": style.TransitionIn.DurationFrames}
		}
		params["animation"] = animation
	}
	if image := style.Image; image != nil {
		if image.Width != nil {
			params["image_width"] = *image.Width
		}
		if image.Height != nil {
			params["image_height"] = *image.Height
		}
		if image.PositionX != nil {
			params["image_position_x"] = *image.PositionX
		}
		if image.PositionY != nil {
			params["image_position_y"] = *image.PositionY
		}
		if image.Radius != nil {
			params["image_radius"] = *image.Radius
		}
		if image.Bounded != nil {
			params["image_bounded"] = *image.Bounded
		}
		if image.Fit != "" {
			params["image_fit"] = image.Fit
		}
		if image.Glow != nil {
			radius, intensity, color := 24.0, 0.25, "#FFFFFF"
			if image.Glow.Radius != nil {
				radius = *image.Glow.Radius
			}
			if image.Glow.Intensity != nil {
				intensity = *image.Glow.Intensity
			}
			if image.Glow.Color != "" {
				color = image.Glow.Color
			}
			if radius > 0 && intensity > 0 {
				params["image_glow_radius"] = radius
				params["image_glow_intensity"] = intensity
				params["image_glow_color"] = color
			}
		}
		if image.Border != nil {
			params["image_border_width"] = image.Border.Width
			if image.Border.Color != "" {
				params["image_border_color"] = image.Border.Color
			}
			if image.Border.Radius != nil {
				params["image_border_radius"] = *image.Border.Radius
			}
		}
		if image.Stroke != nil {
			params["image_stroke_width"] = image.Stroke.Width
			if image.Stroke.Color != "" {
				params["image_stroke_color"] = image.Stroke.Color
			}
		}
		if image.Shadow != nil {
			params["image_shadow_enabled"] = image.Shadow.Enabled
			if image.Shadow.Color != "" {
				params["image_shadow_color"] = image.Shadow.Color
			}
			if image.Shadow.Opacity != nil {
				params["image_shadow_opacity"] = *image.Shadow.Opacity
			}
			if image.Shadow.Blur != nil {
				// Keep the flat override inside the same native GPU range as the
				// capped Frame; frameFromImageParams would otherwise overwrite
				// the 10 px Frame value with the larger flat one.
				params["image_shadow_blur"] = *image.Shadow.Blur
			}
			if len(image.Shadow.Offset) >= 1 {
				params["image_shadow_offset_x"] = image.Shadow.Offset[0]
			}
			if len(image.Shadow.Offset) >= 2 {
				params["image_shadow_offset_y"] = image.Shadow.Offset[1]
			}
		}
	}
	if style.Shadow != nil {
		opacity, blur, offsetX, offsetY := 0.7, 8.0, 0.0, 4.0
		if !style.Shadow.Enabled {
			opacity, blur, offsetX, offsetY = 0, 0, 0, 0
		}
		if style.Shadow.Opacity != nil {
			opacity = *style.Shadow.Opacity
		}
		if style.Shadow.Blur != nil {
			blur = *style.Shadow.Blur
		}
		if len(style.Shadow.Offset) >= 2 {
			offsetX, offsetY = style.Shadow.Offset[0], style.Shadow.Offset[1]
		}
		shadow := map[string]any{}
		if style.Shadow.Color != "" {
			shadow["color"] = style.Shadow.Color
		}
		shadow["opacity"], shadow["blur"] = opacity, blur
		shadow["offset"] = []float64{offsetX, offsetY}
		params["style"] = mergeStyleParam(params["style"], map[string]any{"shadow": shadow})
		params["shadow_opacity"], params["shadow_blur_px"] = opacity, blur
		params["shadow_offset_x_px"], params["shadow_offset_y_px"] = offsetX, offsetY
	}
	return params
}

// ImageFrame projects border, stroke, shadow and radius styling to a plan item.
func ImageFrame(style *scriptpkg.OverlayImageStyleSpec) *capmodel.OverlayItemFrame {
	if style == nil {
		return nil
	}
	frame := &capmodel.OverlayItemFrame{}
	if style.Border != nil && style.Border.Width > 0 {
		frame.Border = &capmodel.OverlayItemFrameBorder{WidthPX: style.Border.Width, Color: style.Border.Color}
		if style.Border.Radius != nil {
			frame.Border.RadiusPX = *style.Border.Radius
		}
	}
	if style.Stroke != nil && style.Stroke.Width > 0 {
		frame.Stroke = &capmodel.OverlayItemFrameStroke{WidthPX: style.Stroke.Width, Color: style.Stroke.Color}
	}
	if style.Shadow != nil && style.Shadow.Enabled {
		color := style.Shadow.Color
		if color == "" {
			color = "#000000"
		}
		// Chronon certifies separable blur on the strict Vulkan path through
		// 10 px. Keep generated image cards inside that native-GPU range.
		shadow := &capmodel.OverlayItemFrameShadow{Color: color, Opacity: 0.7, BlurPX: 10, OffsetYP: 6}
		if style.Shadow.Opacity != nil {
			shadow.Opacity = *style.Shadow.Opacity
		}
		if style.Shadow.Blur != nil {
			shadow.BlurPX = *style.Shadow.Blur
		}
		if len(style.Shadow.Offset) > 0 {
			shadow.OffsetXP = style.Shadow.Offset[0]
		}
		if len(style.Shadow.Offset) > 1 {
			shadow.OffsetYP = style.Shadow.Offset[1]
		}
		frame.Shadow = shadow
	}
	if style.Radius != nil {
		frame.ClipRadiusPX = *style.Radius
	}
	if frame.Border == nil && frame.Shadow == nil && frame.Stroke == nil && frame.ClipRadiusPX == 0 {
		return nil
	}
	return frame
}

// ImageBackdropParams projects the standalone-image backdrop treatment into
// the renderer's params.image_backdrop block. Unset values stay absent so the
// renderer's verified defaults apply; the grid is emitted as its own nested
// object because line/dot geometry is a closed contract.
func ImageBackdropParams(backdrop *scriptpkg.OverlayImageBackdropSpec) map[string]any {
	if backdrop == nil {
		return nil
	}
	params := map[string]any{}
	if backdrop.Zoom != nil {
		params["zoom"] = *backdrop.Zoom
	}
	if backdrop.BlurPX != nil {
		params["blur_px"] = *backdrop.BlurPX
	}
	if grid := backdrop.Grid; grid != nil {
		gridParams := map[string]any{}
		if grid.Enabled != nil {
			gridParams["enabled"] = *grid.Enabled
		}
		if grid.Scope != "" {
			gridParams["scope"] = grid.Scope
		}
		if grid.Style != "" {
			gridParams["style"] = grid.Style
		}
		if grid.Color != "" {
			gridParams["color"] = grid.Color
		}
		if grid.SpacingPX != nil {
			gridParams["spacing_px"] = *grid.SpacingPX
		}
		if grid.LineWidthPX != nil {
			gridParams["line_width_px"] = *grid.LineWidthPX
		}
		if grid.DotRadiusPX != nil {
			gridParams["dot_radius_px"] = *grid.DotRadiusPX
		}
		if grid.Opacity != nil {
			gridParams["opacity"] = *grid.Opacity
		}
		params["grid"] = gridParams
	}
	return params
}

// MergeRuntimeTextStyleParams applies the canonical run-level typography to
// one text-bearing item while preserving its geometry and image controls.
// Payload projection still needs it for chain segments, which are assembled
// after the planner has normalized presentation fonts.
func MergeRuntimeTextStyleParams(params, styleParams map[string]any) map[string]any {
	out := make(map[string]any, len(params)+len(styleParams))
	for key, value := range params {
		out[key] = value
	}
	for key, value := range styleParams {
		if key == "style" {
			if nested, ok := value.(map[string]any); ok {
				out[key] = mergeStyleParam(out[key], nested)
			}
			continue
		}
		if IsRuntimeTextStyleParam(key) {
			out[key] = value
		}
	}
	return out
}

// MergeImageStyle overlays a per-pin image style on the run-level one. Every
// field the pin leaves unset keeps the run-level value, so overlay_images[].
// style names only what is different about that photo; a declared effects
// stack replaces the run-level stack (an empty list is not a declaration).
func MergeImageStyle(base, override *scriptpkg.OverlayImageStyleSpec) *scriptpkg.OverlayImageStyleSpec {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}
	merged := *base
	if override.Width != nil {
		merged.Width = override.Width
	}
	if override.Height != nil {
		merged.Height = override.Height
	}
	if override.PositionX != nil {
		merged.PositionX = override.PositionX
	}
	if override.PositionY != nil {
		merged.PositionY = override.PositionY
	}
	if override.Radius != nil {
		merged.Radius = override.Radius
	}
	if override.Border != nil {
		merged.Border = override.Border
	}
	if override.Stroke != nil {
		merged.Stroke = override.Stroke
	}
	if override.Shadow != nil {
		merged.Shadow = override.Shadow
	}
	if override.Glow != nil {
		merged.Glow = override.Glow
	}
	if override.Bounded != nil {
		merged.Bounded = override.Bounded
	}
	if override.Fit != "" {
		merged.Fit = override.Fit
	}
	if len(override.Effects) > 0 {
		merged.Effects = override.Effects
	}
	if override.Grid != nil {
		merged.Grid = mergeImageGrid(base.Grid, override.Grid)
	}
	if override.Backdrop != nil {
		merged.Backdrop = override.Backdrop
	}
	return &merged
}

// mergeImageGrid overlays the supplied grid keys on the base grid. The grid
// block is all-optional by contract, so a pin can, for example, turn the grid
// off without restating its style, colour or spacing.
func mergeImageGrid(base, override *scriptpkg.OverlayImageGridSpec) *scriptpkg.OverlayImageGridSpec {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}
	merged := *base
	if override.Enabled != nil {
		merged.Enabled = override.Enabled
	}
	if override.Scope != "" {
		merged.Scope = override.Scope
	}
	if override.Style != "" {
		merged.Style = override.Style
	}
	if override.Color != "" {
		merged.Color = override.Color
	}
	if override.SpacingPX != nil {
		merged.SpacingPX = override.SpacingPX
	}
	if override.LineWidthPX != nil {
		merged.LineWidthPX = override.LineWidthPX
	}
	if override.DotRadiusPX != nil {
		merged.DotRadiusPX = override.DotRadiusPX
	}
	if override.Opacity != nil {
		merged.Opacity = override.Opacity
	}
	return &merged
}

func ApplyImageStyle(item *capmodel.OverlayItem, style *scriptpkg.OverlayImageStyleSpec) {
	if item == nil || style == nil {
		return
	}
	params := StyleParams(&scriptpkg.OverlayStyleSpec{Image: style})
	if len(params) > 0 {
		if item.Params == nil {
			item.Params = map[string]any{}
		}
		for key, value := range params {
			if strings.HasPrefix(key, "image_glow_") {
				item.Params[key] = value
				continue
			}
			if strings.HasPrefix(key, "image_") {
				item.Params[strings.TrimPrefix(key, "image_")] = value
			}
		}
	}
	// image_backdrop reuses one asset as a full-canvas blurred background.
	// It is valid only for a standalone image: composites and groups own
	// independent member frames, and RenderingGen rejects the key there.
	if backdrop := ImageBackdropParams(style.Backdrop); backdrop != nil && len(item.ImageLayers) == 0 {
		if item.Params == nil {
			item.Params = map[string]any{}
		}
		item.Params["image_backdrop"] = backdrop
	}
	ApplyImageTreatments(item, style)
	item.Frame = ImageFrame(style)
	for i := range item.ImageLayers {
		if item.ImageLayers[i].Params == nil {
			item.ImageLayers[i].Params = map[string]any{}
		}
		for key, value := range params {
			if strings.HasPrefix(key, "image_glow_") {
				item.ImageLayers[i].Params[key] = value
				continue
			}
			if strings.HasPrefix(key, "image_") {
				item.ImageLayers[i].Params[strings.TrimPrefix(key, "image_")] = value
			}
		}
		item.ImageLayers[i].Frame = ImageFrame(style)
	}
	NormalizeEntityImageLayer(item)
	item.RenderKey = ""
}

// NormalizeEntityImageLayer removes the legacy image radius override while
// preserving the planner's centered hero geometry and caption-safe vertical
// position.
func NormalizeEntityImageLayer(item *capmodel.OverlayItem) {
	if item == nil || item.Kind != string(capmodel.KindEntityImage) {
		return
	}
	if item.Params != nil {
		delete(item.Params, "radius")
	}
	for i := range item.ImageLayers {
		if item.ImageLayers[i].Params != nil {
			delete(item.ImageLayers[i].Params, "radius")
		}
	}
}

// IsImageOverlayKind reports kinds lowered by RenderingGen as image-backed
// overlay content, independent of whether the producer has attached its asset
// refs yet (chain segments own their asset refs separately).
func IsImageOverlayKind(kind string) bool {
	switch kind {
	case "image", string(capmodel.KindEntityImage), string(capmodel.KindEntityCard), string(capmodel.KindImagePopup), string(capmodel.KindProduct), string(capmodel.KindLogo):
		return true
	default:
		return false
	}
}

// IsAssetImageOverlayItem reports image-backed overlay items except map plans.
func IsAssetImageOverlayItem(item capmodel.OverlayItem) bool {
	if item.Map != nil || len(item.AssetRefs) == 0 {
		return false
	}
	return IsImageOverlayKind(item.Kind)
}

// IsRuntimeTextStyleParam reports flat renderer keys for text styling.
func IsRuntimeTextStyleParam(key string) bool {
	switch key {
	case "font_family", "font_size_px", "fill", "glow_size", "glow_color", "stroke_size", "shadow_blur_px", "shadow_opacity", "shadow_offset_x_px", "shadow_offset_y_px",
		"cursor_color", "cursor_width_px", "cursor_height_px", "cursor_blink":
		return true
	default:
		return false
	}
}

// IsTextOverlayKind reports whether an item is a renderer text primitive.
func IsTextOverlayKind(kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	return strings.HasPrefix(kind, "text_") || kind == "phrase" || kind == "important_phrase" ||
		kind == "short_phrase" || kind == "keyword" || kind == "important_word" ||
		kind == "number" || kind == "date_number" || kind == "quote" || kind == "brand_text" || kind == "brand-text"
}

// MergeStyleParam merges keys into an existing style map without aliasing it.
func MergeStyleParam(existing any, add map[string]any) map[string]any {
	return mergeStyleParam(existing, add)
}

func mergeStyleParam(existing any, add map[string]any) map[string]any {
	out := map[string]any{}
	if current, ok := existing.(map[string]any); ok {
		for key, value := range current {
			out[key] = value
		}
	}
	for key, value := range add {
		out[key] = value
	}
	return out
}

func rgbaHex(color []float64) string {
	if len(color) < 3 {
		return ""
	}
	clamp := func(value float64) int {
		if value < 0 {
			value = 0
		}
		if value > 1 {
			value = 1
		}
		return int(value*255 + 0.5)
	}
	return fmt.Sprintf("#%02X%02X%02X", clamp(color[0]), clamp(color[1]), clamp(color[2]))
}
