// Package script — overlay_style_spec.go owns the overlay style-spec
// family. Split from generation_envelope.go (2026-10-13) to keep every file
// under max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
// No behaviour change.
package script

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

// SupportedFontFamilies is the single source of truth for the font family
// IDs a payload may select. The empty string keeps the preset font.
// capabilities/overlays.CanonicalFontAssetsByFamily must expose exactly
// these IDs with their staged path and hash (pinned by parity test); the
// RenderingGen runtime registry must resolve the same IDs. Adding a family
// is one entry here plus its asset entry — never an inline enum elsewhere.
var SupportedFontFamilies = []string{
	"poppins",
	"inter",
	"inter_bold",
	"dejavu_sans",
	"playfair_display_italic",
	"bricolage_grotesque",
	"oswald",
	"abril_fatface",
}

// IsSupportedFontFamily reports whether family is selectable in a payload
// (empty keeps the preset font).
func IsSupportedFontFamily(family string) bool {
	if family == "" {
		return true
	}
	for _, supported := range SupportedFontFamilies {
		if family == supported {
			return true
		}
	}
	return false
}

// OverlayStyleSpec is the canonical script.generate visual override block.
// Every field is optional so existing payloads keep their exact behaviour.
type OverlayStyleSpec struct {
	Shadow       *OverlayShadowSpec     `json:"shadow,omitempty"`
	Color        []float64              `json:"color,omitempty"`
	Size         *OverlaySizeSpec       `json:"size,omitempty"`
	TransitionIn *OverlayTransitionSpec `json:"transition_in,omitempty"`
	// FontFamily is one of RenderingGen's bundled text families. Empty keeps
	// the preset font. Runtime IDs are intentionally closed and path-free.
	FontFamily string `json:"font_family,omitempty"`
	// GlowSize and StrokeSize are pixel widths. Explicit zero disables the
	// corresponding effect; nil preserves the preset's value.
	GlowSize *float64 `json:"glow_size,omitempty"`
	// GlowColor explicitly replaces a motion preset's glow color while keeping
	// its authored radius and intensity.
	GlowColor  string   `json:"glow_color,omitempty"`
	StrokeSize *float64 `json:"stroke_size,omitempty"`
	// Cursor overrides the typewriter caret rendered alongside phrase
	// overlays: its chosen color, its pixel geometry (width = thickness,
	// height = pixel size) and its blink behaviour. Nil preserves the
	// motion-authored caret; explicit zero width hides it.
	Cursor *OverlayCursorStyleSpec `json:"cursor,omitempty"`
	// Image controls apply to generated image overlays and entity portraits.
	// They stay separate from text styling so one script can tune both layers.
	Image *OverlayImageStyleSpec `json:"image,omitempty"`
}

// OverlayCursorStyleSpec controls the runtime typewriter caret.
// Color is #RRGGBB; Width is the caret thickness in output pixels;
// Height is the caret pixel height; Blink opts into the post-type blink.
type OverlayCursorStyleSpec struct {
	Color  string   `json:"color,omitempty"`
	Width  *float64 `json:"width,omitempty"`
	Height *float64 `json:"height,omitempty"`
	Blink  *bool    `json:"blink,omitempty"`
}

// OverlayImageStyleSpec controls image-card geometry and its visible frame.
// Geometry uses output pixels; position is relative to the canvas center.
// Bounded, when true, clips the card inside its authored box (fit controls
// how the pixels fill that box); nil preserves the preset behaviour.
type OverlayImageStyleSpec struct {
	Width     *int                `json:"width,omitempty"`
	Height    *int                `json:"height,omitempty"`
	PositionX *float64            `json:"position_x,omitempty"`
	PositionY *float64            `json:"position_y,omitempty"`
	Radius    *float64            `json:"radius,omitempty"`
	Border    *OverlayImageBorder `json:"border,omitempty"`
	Stroke    *OverlayImageStroke `json:"stroke,omitempty"`
	Shadow    *OverlayShadowSpec  `json:"shadow,omitempty"`
	Bounded   *bool               `json:"bounded,omitempty"`
	Fit       string              `json:"fit,omitempty"`
}

type OverlayImageBorder struct {
	Width  float64  `json:"width"`
	Color  string   `json:"color"`
	Radius *float64 `json:"radius,omitempty"`
}

type OverlayImageStroke struct {
	Width float64 `json:"width"`
	Color string  `json:"color"`
}

// Validate checks caller-controlled text runtime overrides before they reach
// the RenderingGen queue. RenderingGen repeats these checks at its own trust
// boundary; this validation gives script.generate an early, actionable error.
func (s *OverlayStyleSpec) Validate() error {
	if s == nil {
		return nil
	}
	if !IsSupportedFontFamily(s.FontFamily) {
		return fmt.Errorf("overlay_style.font_family %q is unsupported (%s)", s.FontFamily, strings.Join(SupportedFontFamilies, ", "))
	}
	if s.GlowSize != nil && (math.IsNaN(*s.GlowSize) || math.IsInf(*s.GlowSize, 0) || *s.GlowSize < 0 || *s.GlowSize > 256) {
		return fmt.Errorf("overlay_style.glow_size must be between 0 and 256 pixels")
	}
	if s.GlowColor != "" && !isStyleHexColor(s.GlowColor) {
		return fmt.Errorf("overlay_style.glow_color must be #RRGGBB")
	}
	if s.StrokeSize != nil && (math.IsNaN(*s.StrokeSize) || math.IsInf(*s.StrokeSize, 0) || *s.StrokeSize < 0 || *s.StrokeSize > 64) {
		return fmt.Errorf("overlay_style.stroke_size must be between 0 and 64 pixels")
	}
	if s.Size != nil && s.Size.FontSize != nil && (math.IsNaN(*s.Size.FontSize) || math.IsInf(*s.Size.FontSize, 0) || *s.Size.FontSize <= 0 || *s.Size.FontSize > 512) {
		return fmt.Errorf("overlay_style.size.font_size must be greater than 0 and at most 512 pixels")
	}
	if cursor := s.Cursor; cursor != nil {
		if cursor.Color != "" && !isStyleHexColor(cursor.Color) {
			return fmt.Errorf("overlay_style.cursor.color must be #RRGGBB")
		}
		if cursor.Width != nil && (math.IsNaN(*cursor.Width) || math.IsInf(*cursor.Width, 0) || *cursor.Width < 0 || *cursor.Width > 64) {
			return fmt.Errorf("overlay_style.cursor.width must be between 0 and 64 pixels")
		}
		if cursor.Height != nil && (math.IsNaN(*cursor.Height) || math.IsInf(*cursor.Height, 0) || *cursor.Height < 0 || *cursor.Height > 256) {
			return fmt.Errorf("overlay_style.cursor.height must be between 0 and 256 pixels")
		}
	}
	if image := s.Image; image != nil {
		if image.Fit != "" && image.Fit != "cover" && image.Fit != "contain" && image.Fit != "fill" {
			return fmt.Errorf("overlay_style.image.fit must be cover, contain or fill")
		}
		for name, value := range map[string]*int{"width": image.Width, "height": image.Height} {
			if value != nil && (*value <= 0 || *value > 8192) {
				return fmt.Errorf("overlay_style.image.%s must be between 1 and 8192 pixels", name)
			}
		}
		for name, value := range map[string]*float64{"position_x": image.PositionX, "position_y": image.PositionY} {
			if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || math.Abs(*value) > 8192) {
				return fmt.Errorf("overlay_style.image.%s must be within ±8192 pixels", name)
			}
		}
		if image.Radius != nil && (math.IsNaN(*image.Radius) || math.IsInf(*image.Radius, 0) || *image.Radius < 0 || *image.Radius > 512) {
			return fmt.Errorf("overlay_style.image.radius must be between 0 and 512 pixels")
		}
		if image.Border != nil {
			if math.IsNaN(image.Border.Width) || math.IsInf(image.Border.Width, 0) || image.Border.Width < 0 || image.Border.Width > 512 {
				return fmt.Errorf("overlay_style.image.border.width must be between 0 and 512 pixels")
			}
			if image.Border.Width > 0 && !isStyleHexColor(image.Border.Color) {
				return fmt.Errorf("overlay_style.image.border.color must be #RRGGBB when border width is positive")
			}
			if image.Border.Radius != nil && (math.IsNaN(*image.Border.Radius) || math.IsInf(*image.Border.Radius, 0) || *image.Border.Radius < 0 || *image.Border.Radius > 512) {
				return fmt.Errorf("overlay_style.image.border.radius must be between 0 and 512 pixels")
			}
		}
		if image.Stroke != nil {
			if math.IsNaN(image.Stroke.Width) || math.IsInf(image.Stroke.Width, 0) || image.Stroke.Width < 0 || image.Stroke.Width > 64 {
				return fmt.Errorf("overlay_style.image.stroke.width must be between 0 and 64 pixels")
			}
			if image.Stroke.Width > 0 && !isStyleHexColor(image.Stroke.Color) {
				return fmt.Errorf("overlay_style.image.stroke.color must be #RRGGBB when stroke width is positive")
			}
		}
		if image.Shadow != nil {
			if image.Shadow.Opacity != nil && (math.IsNaN(*image.Shadow.Opacity) || math.IsInf(*image.Shadow.Opacity, 0) || *image.Shadow.Opacity < 0 || *image.Shadow.Opacity > 1) {
				return fmt.Errorf("overlay_style.image.shadow.opacity must be between 0 and 1")
			}
			if image.Shadow.Blur != nil && (math.IsNaN(*image.Shadow.Blur) || math.IsInf(*image.Shadow.Blur, 0) || *image.Shadow.Blur < 0 || *image.Shadow.Blur > 256) {
				return fmt.Errorf("overlay_style.image.shadow.blur must be between 0 and 256 pixels")
			}
			if len(image.Shadow.Offset) > 2 {
				return fmt.Errorf("overlay_style.image.shadow.offset accepts at most [x,y]")
			}
			for _, offset := range image.Shadow.Offset {
				if math.IsNaN(offset) || math.IsInf(offset, 0) || math.Abs(offset) > 256 {
					return fmt.Errorf("overlay_style.image.shadow.offset values must be within ±256 pixels")
				}
			}
			if image.Shadow.Color != "" && !isStyleHexColor(image.Shadow.Color) {
				return fmt.Errorf("overlay_style.image.shadow.color must be #RRGGBB")
			}
		}
	}
	return nil
}

func isStyleHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	_, err := hex.DecodeString(value[1:])
	return err == nil
}

type OverlayShadowSpec struct {
	Enabled bool      `json:"enabled,omitempty"`
	Color   string    `json:"color,omitempty"`
	Opacity *float64  `json:"opacity,omitempty"`
	Blur    *float64  `json:"blur,omitempty"`
	Offset  []float64 `json:"offset,omitempty"`
}

type OverlaySizeSpec struct {
	Width    *int     `json:"width,omitempty"`
	Height   *int     `json:"height,omitempty"`
	FontSize *float64 `json:"font_size,omitempty"`
}

type OverlayTransitionSpec struct {
	Preset         string `json:"preset"`
	DurationFrames int    `json:"duration_frames,omitempty"`
}
