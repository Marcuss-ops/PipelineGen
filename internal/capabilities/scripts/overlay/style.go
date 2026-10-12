// Package overlay owns overlay-specific policy used by the scripts capability.
package overlay

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// BackgroundFromPayload projects a kernel background into the render plan
// domain while retaining verified asset references and style fields.
func BackgroundFromPayload(src *scriptpkg.OverlayBackgroundSpec) *capabilityoverlay.OverlayBackground {
	if src == nil {
		return nil
	}
	background := &capabilityoverlay.OverlayBackground{
		Kind: src.Kind, Color: append([]float64(nil), src.Color...), Fit: src.Fit,
		Opacity: src.Opacity, Loop: src.Loop,
	}
	if params := StyleParams(src.Style); params != nil {
		if style, ok := params["style"].(map[string]any); ok {
			background.Style = style
		}
	}
	if src.AssetID != "" || src.URL != "" || src.SHA256 != "" {
		background.AssetRefs = []capabilityoverlay.OverlayAssetRef{{
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
		params["color"] = append([]float64(nil), style.Color...)
		params["style"] = map[string]any{"fill": rgbaHex(style.Color)}
	}
	if style.Size != nil {
		if style.Size.Width != nil {
			params["box_width"] = *style.Size.Width
		}
		if style.Size.Height != nil {
			params["box_height"] = *style.Size.Height
		}
		if style.Size.FontSize != nil {
			params["style"] = mergeStyleParam(params["style"], map[string]any{"font_size": *style.Size.FontSize})
			params["font_size_px"] = *style.Size.FontSize
		}
	}
	if style.FontFamily != "" {
		params["font_family"] = style.FontFamily
	}
	if style.GlowSize != nil {
		params["glow_size"] = *style.GlowSize
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
func ImageFrame(style *scriptpkg.OverlayImageStyleSpec) *capabilityoverlay.OverlayItemFrame {
	if style == nil {
		return nil
	}
	frame := &capabilityoverlay.OverlayItemFrame{}
	if style.Border != nil && style.Border.Width > 0 {
		frame.Border = &capabilityoverlay.OverlayItemFrameBorder{WidthPX: style.Border.Width, Color: style.Border.Color}
		if style.Border.Radius != nil {
			frame.Border.RadiusPX = *style.Border.Radius
		}
	}
	if style.Stroke != nil && style.Stroke.Width > 0 {
		frame.Stroke = &capabilityoverlay.OverlayItemFrameStroke{WidthPX: style.Stroke.Width, Color: style.Stroke.Color}
	}
	if style.Shadow != nil && style.Shadow.Enabled {
		color := style.Shadow.Color
		if color == "" {
			color = "#000000"
		}
		// Chronon certifies separable blur on the strict Vulkan path through
		// 10 px. Keep generated image cards inside that native-GPU range.
		shadow := &capabilityoverlay.OverlayItemFrameShadow{Color: color, Opacity: 0.7, BlurPX: 10, OffsetYP: 6}
		if style.Shadow.Opacity != nil {
			shadow.Opacity = *style.Shadow.Opacity
		}
		if style.Shadow.Blur != nil {
			shadow.BlurPX = min(*style.Shadow.Blur, 10)
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

// ImageStyleParams returns the image-specific runtime overrides from the
// same style projection used for standalone image items.
func ImageStyleParams(style *scriptpkg.OverlayImageStyleSpec) map[string]any {
	all := StyleParams(&scriptpkg.OverlayStyleSpec{Image: style})
	out := make(map[string]any)
	for key, value := range all {
		if strings.HasPrefix(key, "image_") {
			out[strings.TrimPrefix(key, "image_")] = value
		}
	}
	return out
}

// ApplyImageStyle applies runtime image params and frame metadata to the item
// and its image layers, then normalizes entity portrait geometry.
func MergeCaptionStyleIntoLayers(layers []capabilityoverlay.OverlayImageLayer, styleParams map[string]any) {
	if len(styleParams) == 0 {
		return
	}
	for index := range layers {
		if strings.TrimSpace(layers[index].Caption) == "" {
			continue
		}
		layers[index].Params = MergeRuntimeTextStyleParams(layers[index].Params, styleParams)
	}
}

// MergeRuntimeTextStyleParams applies the canonical run-level typography to
// one text-bearing item while preserving its geometry and image controls.
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

func ApplyImageStyle(item *capabilityoverlay.OverlayItem, style *scriptpkg.OverlayImageStyleSpec) {
	if item == nil || style == nil {
		return
	}
	params := StyleParams(&scriptpkg.OverlayStyleSpec{Image: style})
	if len(params) > 0 {
		if item.Params == nil {
			item.Params = map[string]any{}
		}
		for key, value := range params {
			if strings.HasPrefix(key, "image_") {
				item.Params[strings.TrimPrefix(key, "image_")] = value
			}
		}
	}
	item.Frame = ImageFrame(style)
	for i := range item.ImageLayers {
		if item.ImageLayers[i].Params == nil {
			item.ImageLayers[i].Params = map[string]any{}
		}
		for key, value := range params {
			if strings.HasPrefix(key, "image_") {
				item.ImageLayers[i].Params[strings.TrimPrefix(key, "image_")] = value
			}
		}
		item.ImageLayers[i].Frame = ImageFrame(style)
	}
	NormalizeEntityImageLayer(item)
	item.RenderKey = ""
}

// NormalizeEntityImageLayer removes legacy geometry overrides that distort
// entity portraits while retaining the shared image frame and backing panel.
func NormalizeEntityImageLayer(item *capabilityoverlay.OverlayItem) {
	if item == nil || item.Kind != string(capabilityoverlay.KindEntityImage) {
		return
	}
	if item.Params != nil {
		delete(item.Params, "radius")
		delete(item.Params, "position_y")
	}
	for i := range item.ImageLayers {
		if item.ImageLayers[i].Params != nil {
			delete(item.ImageLayers[i].Params, "radius")
			delete(item.ImageLayers[i].Params, "position_y")
		}
	}
}

// IsImageOverlayKind reports kinds lowered by RenderingGen as image-backed
// overlay content, independent of whether the producer has attached its asset
// refs yet (chain segments own their asset refs separately).
func IsImageOverlayKind(kind string) bool {
	switch kind {
	case "image", string(capabilityoverlay.KindEntityImage), string(capabilityoverlay.KindEntityCard), string(capabilityoverlay.KindImagePopup), string(capabilityoverlay.KindProduct), string(capabilityoverlay.KindLogo):
		return true
	default:
		return false
	}
}

// IsImageOverlayItem reports image-backed overlay items except map plans.
func IsImageOverlayItem(item capabilityoverlay.OverlayItem) bool {
	if item.Map != nil || len(item.AssetRefs) == 0 {
		return false
	}
	return IsImageOverlayKind(item.Kind)
}

// IsRuntimeTextStyleParam reports flat renderer keys for text styling.
func IsRuntimeTextStyleParam(key string) bool {
	switch key {
	case "font_family", "font_size_px", "glow_size", "stroke_size", "shadow_blur_px", "shadow_opacity", "shadow_offset_x_px", "shadow_offset_y_px",
		"cursor_color", "cursor_width_px", "cursor_height_px", "cursor_blink":
		return true
	default:
		return false
	}
}

// IsRuntimeCursorParam reports the cursor caret overrides.
func IsRuntimeCursorParam(key string) bool {
	switch key {
	case "cursor_color", "cursor_width_px", "cursor_height_px", "cursor_blink":
		return true
	default:
		return false
	}
}

// IsRuntimeImageParam reports the image-card runtime overrides.
func IsRuntimeImageParam(key string) bool {
	switch key {
	case "image_width", "image_height", "image_position_x", "image_position_y", "image_radius",
		"image_bounded", "image_fit",
		"image_border_width", "image_border_color", "image_border_radius",
		"image_stroke_width", "image_stroke_color",
		"image_shadow_enabled", "image_shadow_color", "image_shadow_opacity", "image_shadow_blur",
		"image_shadow_offset_x", "image_shadow_offset_y":
		return true
	default:
		return false
	}
}

// IsTextOverlayKind reports whether an item is a renderer text primitive.
func IsTextOverlayKind(kind string) bool {
	return strings.HasPrefix(kind, "text_") || kind == "number" || kind == "quote" || kind == "brand_text"
}

// StyleFontColor projects an explicit RGBA style color to the caption's
// supported hex fill. An omitted color remains omitted so entity preset color
// can remain the fallback.
func StyleFontColor(style *scriptpkg.OverlayStyleSpec) string {
	if style == nil || len(style.Color) < 3 {
		return ""
	}
	return rgbaHex(style.Color)
}

// IsEntityOverlayKind reports entity card and entity image primitives.
func IsEntityOverlayKind(kind string) bool {
	return kind == string(capabilityoverlay.KindEntityCard) || kind == string(capabilityoverlay.KindEntityImage)
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
