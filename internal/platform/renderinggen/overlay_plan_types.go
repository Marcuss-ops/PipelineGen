// overlay_plan_types.go — overlay-plan wire types, split out of clip_plan_mapper.go (600-line file gate).
package renderinggen

import (
	"encoding/json"
	"math"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// It mirrors the semanticPlan struct in RenderingGen's compiler package so
// changes to either side surface as compile/unmarshal errors immediately.
type overlayPlan struct {
	SchemaVersion     string                      `json:"schema_version"`
	PlanID            string                      `json:"plan_id"`
	VideoID           string                      `json:"video_id"`
	Width             int                         `json:"width"`
	Height            int                         `json:"height"`
	FPSNum            int                         `json:"fps_num"`
	FPSDen            int                         `json:"fps_den"`
	DurationMS        int64                       `json:"duration_ms,omitempty"`
	OutputProfileID   string                      `json:"output_profile_id,omitempty"`
	AnimationPolicies []scriptpkg.AnimationPolicy `json:"animation_policies,omitempty"`
	Source            *overlaySource              `json:"source,omitempty"`
	ForegroundScale   int                         `json:"foreground_scale_percent,omitempty"`
	SourceFrame       *sourceFrameBlock           `json:"source_frame,omitempty"`
	ClipEffects       []map[string]any            `json:"clip_effects,omitempty"`
	ClipGrid          *clipGridBlock              `json:"clip_grid,omitempty"`
	Background        *overlayBackground          `json:"background,omitempty"`
	Subtitles         *overlaySubtitles           `json:"subtitles,omitempty"`
	Watermark         *overlayWatermark           `json:"watermark,omitempty"`
	Audio             *overlayAudio               `json:"audio,omitempty"`
	Items             []json.RawMessage           `json:"items"`
}

type overlaySource struct {
	AssetID string `json:"asset_id"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

// sourceFrameBlock is the wire projection of the plan's card treatment. It
// mirrors RenderingGen's overlay contract field-for-field (border width/colour/
// radius + shadow + stroke), so a declared frame is transported verbatim and
// never re-interpreted here.
type clipGridBlock struct {
	Style       string   `json:"style,omitempty"`
	SpacingPX   float64  `json:"spacing_px"`
	LineWidthPX *float64 `json:"line_width_px,omitempty"`
	DotRadiusPX *float64 `json:"dot_radius_px,omitempty"`
	Color       string   `json:"color"`
	Opacity     float64  `json:"opacity"`
}

type sourceFrameBlock struct {
	Border       *sourceFrameBorder `json:"border,omitempty"`
	Shadow       *sourceFrameShadow `json:"shadow,omitempty"`
	Stroke       *sourceFrameStroke `json:"stroke,omitempty"`
	ClipRadiusPX float64            `json:"clip_radius_px,omitempty"`
}

type sourceFrameBorder struct {
	WidthPX  float64 `json:"width_px"`
	Color    string  `json:"color"`
	RadiusPX float64 `json:"radius_px,omitempty"`
}

type sourceFrameShadow struct {
	Color    string  `json:"color"`
	Opacity  float64 `json:"opacity,omitempty"`
	BlurPX   float64 `json:"blur_px,omitempty"`
	OffsetXP float64 `json:"offset_x_px,omitempty"`
	OffsetYP float64 `json:"offset_y_px,omitempty"`
}

type sourceFrameStroke struct {
	WidthPX float64 `json:"width_px"`
	Color   string  `json:"color"`
}

// mapSourceFrame projects the sealed plan's source_frame verbatim. The
// producer has already validated it (cliprender.ValidatePlanSourceFrame), so a
// value that reaches here has already been range- and colour-checked at the
// request AND at the sealed-plan boundary.
func mapSourceFrame(frame *cliprender.PlanSourceFrame) *sourceFrameBlock {
	if frame == nil {
		return nil
	}
	out := &sourceFrameBlock{
		ClipRadiusPX: frame.ClipRadiusPX,
	}
	if frame.Border != nil {
		out.Border = &sourceFrameBorder{
			WidthPX:  frame.Border.WidthPX,
			Color:    frame.Border.Color,
			RadiusPX: frame.Border.RadiusPX,
		}
	}
	if frame.Shadow != nil {
		out.Shadow = &sourceFrameShadow{
			Color:    frame.Shadow.Color,
			Opacity:  frame.Shadow.Opacity,
			BlurPX:   frame.Shadow.BlurPX,
			OffsetXP: frame.Shadow.OffsetXP,
			OffsetYP: frame.Shadow.OffsetYP,
		}
	}
	if frame.Stroke != nil {
		out.Stroke = &sourceFrameStroke{
			WidthPX: frame.Stroke.WidthPX,
			Color:   frame.Stroke.Color,
		}
	}
	return out
}

type overlayBackground struct {
	Kind      string            `json:"kind"`
	AssetRefs []overlayAssetRef `json:"asset_refs,omitempty"`
	Color     []float64         `json:"color,omitempty"`
	Fit       string            `json:"fit,omitempty"`
	Opacity   *float64          `json:"opacity,omitempty"`
	Loop      bool              `json:"loop,omitempty"`
}

type overlaySubtitles struct {
	AssetRefs []overlayAssetRef `json:"asset_refs,omitempty"`
	StyleID   string            `json:"style_id,omitempty"`
	Mode      string            `json:"mode,omitempty"`
	// Style is the caller's typed visual override (font, size, color, shadow,
	// position, width…). It MUST travel with the plan: dropping it here
	// historically caused the RenderingGen compiler to fall back to its own
	// hardcoded defaults, silently diverging from the requested style.
	Style *styleBlock `json:"style,omitempty"`
}

type overlayWatermark struct {
	Text      string            `json:"text,omitempty"`
	AssetRefs []overlayAssetRef `json:"asset_refs,omitempty"`
	FontRef   *overlayAssetRef  `json:"font_ref,omitempty"`
	Position  string            `json:"position,omitempty"`
	Opacity   *float64          `json:"opacity,omitempty"`
	// MarginPX is the requested distance from the canvas edge. It is a *int so
	// an explicit 0 stays distinguishable from an unset value.
	MarginPX *int        `json:"margin_px,omitempty"`
	Style    *styleBlock `json:"style,omitempty"`
}

// styleBlock is the wire projection of the canonical kernel/script
// VideoVisualStyleSpec. It mirrors that struct field-for-field so the typed
// owner in kernel/script remains the single source of truth; the mapper only
// serialises it.
type styleBlock struct {
	Font         string           `json:"font,omitempty"`
	Position     string           `json:"position,omitempty"`
	Size         float64          `json:"size,omitempty"`
	Color        string           `json:"color,omitempty"`
	FontSizePX   float64          `json:"font_size_px,omitempty"`
	WidthPX      int              `json:"width_px,omitempty"`
	HeightPX     int              `json:"height_px,omitempty"`
	ScalePercent float64          `json:"scale_percent,omitempty"`
	Stroke       *strokeBlock     `json:"stroke,omitempty"`
	Shadow       *shadowBlock     `json:"shadow,omitempty"`
	TransitionIn *transitionBlock `json:"transition_in,omitempty"`
}

type shadowBlock struct {
	Color   string  `json:"color,omitempty"`
	Opacity float64 `json:"opacity,omitempty"`
	BlurPX  float64 `json:"blur_px,omitempty"`
	OffsetX float64 `json:"offset_x,omitempty"`
	OffsetY float64 `json:"offset_y,omitempty"`
}

type strokeBlock struct {
	Color string  `json:"color,omitempty"`
	Width float64 `json:"width,omitempty"`
}

type transitionBlock struct {
	Preset     string `json:"preset,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// marshalStyle projects the canonical kernel/script visual style into the
// wire block. A nil style stays nil on the wire (no empty object emitted).
// derivedKeylineWidth sizes the black contour the mapper derives when a caller
// declares a shadow but no stroke. It scales with the font (about 4.5% of the
// em) instead of the historical fixed 5 px: at caption line widths of ~40
// runes, a 5 px keyline merges the contours of adjacent glyphs into a solid
// black smear, which is what made burned captions unreadable. Explicit
// strokes declared by the caller are never rescaled.
func derivedKeylineWidth(in *scriptpkg.VideoVisualStyleSpec) float64 {
	size := in.FontSizePX
	if size <= 0 {
		size = in.Size
	}
	if size <= 0 {
		size = 54
	}
	w := math.Round(size * 0.045)
	if w < 1.5 {
		w = 1.5
	}
	if w > 3 {
		w = 3
	}
	return w
}

// subtitleLineHeightFactor converts a font size into the line box used for
// burned captions (Chronon centres the text inside this box).
