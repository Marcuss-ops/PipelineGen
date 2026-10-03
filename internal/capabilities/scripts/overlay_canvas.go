// Package scriptgeneration — overlay_canvas.go owns the render-canvas value
// object and its style-projection helpers for the semantic OverlayPlan
// derivation. It is the cohesive canvas/style sibling of overlay_plan.go:
// the canvas is the run-level render context (dimensions, background,
// typography, motion pools) that CompileOverlayPlan receives, and the
// helpers project OverlayStyleSpec into the plan's param map.
//
// Split from overlay_plan.go (625 → <600) to satisfy the strict 600-LOC
// forward-prevention gate (godlike/08) without changing behaviour. The two
// files share package scriptgeneration and no new package is introduced.
package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// OverlayCanvasSpec is the target render canvas for the derived OverlayPlan.
// The runner's withDefaults() resolves a zero spec to the production contract
// (1920×1080 @ 24/1), matching the AssemblyReadyVideoContract.
// Golden/certification tests use GoldenOverlayCanvas (1280×720 @ 30/1) explicitly.
type OverlayCanvasSpec struct {
	Width                  int
	Height                 int
	FPSNum                 int
	FPSDen                 int
	ForegroundScalePercent int
	Background             *capabilityoverlay.OverlayBackground
	Style                  *scriptpkg.OverlayStyleSpec
	// PhraseMotions is the run's phrase-motion rotation pool, carried from
	// the request's channel profile (empty = the certified default pool). It
	// lives here because the canvas is the run-level render context this
	// function already receives; the planner validates the ids fail-closed.
	PhraseMotions      []string
	PhraseMotionFamily string
	ImageMotions       []string
	// MaxPhraseOverlays overrides the run-level grounded-phrase ceiling for
	// this render. It is caller-supplied (request max_phrase_overlays); zero
	// keeps the certified default (capabilityoverlay.MaxPhraseOverlaysPerRun).
	MaxPhraseOverlays int
	MapsOnly          bool
	// MaxImageOverlays overrides the run-level image ceiling; zero keeps the
	// certified default (capabilityoverlay.MaxImageOverlaysPerRun).
	MaxImageOverlays      int
	DisableNumberOverlays bool
}

// GoldenOverlayCanvas is the validated golden canary canvas (1280×720,
// 30/1 FPS, 5 seconds of job) — the same canvas the cross-repo canary renders.
// Production runners derive from the AssemblyReadyVideoContract (1920×1080 @ 24/1).
var GoldenOverlayCanvas = OverlayCanvasSpec{Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1}

func (c OverlayCanvasSpec) withDefaults() OverlayCanvasSpec {
	if c.Width <= 0 || c.Height <= 0 || c.FPSNum <= 0 || c.FPSDen <= 0 {
		// Production default: derived from the AssemblyReadyVideoContract
		// (1920×1080 @ 24/1). Golden/certification paths use GoldenOverlayCanvas
		// explicitly. Preserve all caller-owned semantic fields: the old
		// replacement silently dropped Background and Style whenever the
		// production canvas dimensions were left at zero.
		c.Width, c.Height, c.FPSNum, c.FPSDen = 1920, 1080, 24, 1
	}
	return c
}

func overlayBackgroundFromPayload(src *scriptpkg.OverlayBackgroundSpec) *capabilityoverlay.OverlayBackground {
	if src == nil {
		return nil
	}
	bg := &capabilityoverlay.OverlayBackground{
		Kind: src.Kind, Color: append([]float64(nil), src.Color...), Fit: src.Fit, Opacity: src.Opacity, Loop: src.Loop,
	}
	if params := overlayStyleParams(src.Style); params != nil {
		if style, ok := params["style"].(map[string]any); ok {
			bg.Style = style
		}
	}
	if src.AssetID != "" || src.URL != "" || src.SHA256 != "" {
		bg.AssetRefs = []capabilityoverlay.OverlayAssetRef{{
			AssetID: src.AssetID, URL: src.URL, LocalPath: src.LocalPath, SHA256: src.SHA256, MediaType: src.MediaType,
		}}
	}
	return bg
}

func overlayStyleParams(style *scriptpkg.OverlayStyleSpec) map[string]any {
	if style == nil {
		return nil
	}
	p := map[string]any{}
	if len(style.Color) > 0 {
		p["color"] = append([]float64(nil), style.Color...)
		styleMap := map[string]any{"fill": rgbaHex(style.Color)}
		p["style"] = styleMap
	}
	if style.Size != nil {
		if style.Size.Width != nil {
			p["box_width"] = *style.Size.Width
		}
		if style.Size.Height != nil {
			p["box_height"] = *style.Size.Height
		}
		if style.Size.FontSize != nil {
			p["style"] = mergeStyleParam(p["style"], map[string]any{"font_size": *style.Size.FontSize})
			p["font_size_px"] = *style.Size.FontSize
		}
	}
	if style.FontFamily != "" {
		p["font_family"] = style.FontFamily
	}
	if style.GlowSize != nil {
		p["glow_size"] = *style.GlowSize
	}
	if style.StrokeSize != nil {
		p["stroke_size"] = *style.StrokeSize
	}
	if style.TransitionIn != nil && strings.TrimSpace(style.TransitionIn.Preset) != "" {
		anim := map[string]any{"preset": style.TransitionIn.Preset}
		if style.TransitionIn.DurationFrames > 0 {
			anim["enter"] = map[string]any{"duration_frames": style.TransitionIn.DurationFrames}
		}
		p["animation"] = anim
	}
	if image := style.Image; image != nil {
		if image.Width != nil {
			p["image_width"] = *image.Width
		}
		if image.Height != nil {
			p["image_height"] = *image.Height
		}
		if image.PositionX != nil {
			p["image_position_x"] = *image.PositionX
		}
		if image.PositionY != nil {
			p["image_position_y"] = *image.PositionY
		}
		if image.Radius != nil {
			p["image_radius"] = *image.Radius
		}
	}
	if style.Shadow != nil && style.Shadow.Enabled {
		shadow := map[string]any{}
		if style.Shadow.Color != "" {
			shadow["color"] = style.Shadow.Color
		}
		if style.Shadow.Opacity != nil {
			shadow["opacity"] = *style.Shadow.Opacity
		}
		if style.Shadow.Blur != nil {
			shadow["blur"] = *style.Shadow.Blur
		}
		if len(style.Shadow.Offset) > 0 {
			shadow["offset"] = append([]float64(nil), style.Shadow.Offset...)
		}
		p["style"] = mergeStyleParam(p["style"], map[string]any{"shadow": shadow})
	}
	return p
}

func overlayImageFrame(style *scriptpkg.OverlayImageStyleSpec) *capabilityoverlay.OverlayItemFrame {
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
		shadow := &capabilityoverlay.OverlayItemFrameShadow{Color: color, Opacity: 0.7, BlurPX: 12, OffsetYP: 6}
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

func rgbaHex(color []float64) string {
	if len(color) < 3 {
		return ""
	}
	clamp := func(v float64) int {
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		return int(v*255 + 0.5)
	}
	return fmt.Sprintf("#%02X%02X%02X", clamp(color[0]), clamp(color[1]), clamp(color[2]))
}

func isImageOverlayItem(item capabilityoverlay.OverlayItem) bool {
	if item.Map != nil || len(item.AssetRefs) == 0 {
		return false
	}
	switch item.Kind {
	case string(capabilityoverlay.KindEntityImage), string(capabilityoverlay.KindEntityCard), string(capabilityoverlay.KindImagePopup), string(capabilityoverlay.KindProduct), string(capabilityoverlay.KindLogo):
		return true
	default:
		return false
	}
}

func applyOverlayImageStyle(item *capabilityoverlay.OverlayItem, style *scriptpkg.OverlayImageStyleSpec) {
	if item == nil || style == nil {
		return
	}
	params := overlayStyleParams(&scriptpkg.OverlayStyleSpec{Image: style})
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
	item.Frame = overlayImageFrame(style)
	for i := range item.ImageLayers {
		if item.ImageLayers[i].Params == nil {
			item.ImageLayers[i].Params = map[string]any{}
		}
		for key, value := range params {
			if strings.HasPrefix(key, "image_") {
				item.ImageLayers[i].Params[strings.TrimPrefix(key, "image_")] = value
			}
		}
		item.ImageLayers[i].Frame = overlayImageFrame(style)
	}
	item.RenderKey = ""
}

func isRuntimeTextStyleParam(key string) bool {
	switch key {
	case "font_family", "font_size_px", "glow_size", "stroke_size":
		return true
	default:
		return false
	}
}

func isTextOverlayKind(kind string) bool {
	return strings.HasPrefix(kind, "text_") || kind == "number" || kind == "quote" || kind == "brand_text"
}

func mergeStyleParam(existing any, add map[string]any) map[string]any {
	out := map[string]any{}
	if current, ok := existing.(map[string]any); ok {
		for k, v := range current {
			out[k] = v
		}
	}
	for k, v := range add {
		out[k] = v
	}
	return out
}
