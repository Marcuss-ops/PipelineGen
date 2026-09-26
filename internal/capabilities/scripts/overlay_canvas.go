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
