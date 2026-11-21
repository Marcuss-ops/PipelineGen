// Package script — overlay_style_spec.go owns the overlay style-spec
// family. Split from generation_envelope.go (2026-10-13) to keep every file
// under max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
// No behaviour change.
package script

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/color"
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

// BulletFontSizeMinPX/MaxPX are the pixel sizes the bullet lane may use.
// They are the same bounds the per-bullet editorial pins enforce; the
// capabilities/overlays validator aliases these constants so the payload gate
// and the compiler cannot drift.
const (
	BulletFontSizeMinPX = 24.0
	BulletFontSizeMaxPX = 240.0
)

// OverlayStyleSpec is the canonical script.generate visual override block.
// Every field is optional so existing payloads keep their exact behaviour.
//
// The struct carries BOTH wire tags on purpose: `json` is the published
// renderinggen.overlay-plan.v1 contract, `yaml` is the operator-facing channel
// profile document (config/channel_profiles.yaml). `capabilities/channelprofile`
// decodes into this type directly, so a field added here is immediately
// addressable from a profile and the two shapes cannot drift.
type OverlayStyleSpec struct {
	Shadow       *OverlayShadowSpec     `json:"shadow,omitempty" yaml:"shadow,omitempty"`
	Color        []float64              `json:"color,omitempty" yaml:"color,omitempty"`
	Size         *OverlaySizeSpec       `json:"size,omitempty" yaml:"size,omitempty"`
	TransitionIn *OverlayTransitionSpec `json:"transition_in,omitempty" yaml:"transition_in,omitempty"`
	// FontFamily is one of RenderingGen's bundled text families. Empty keeps
	// the preset font. Runtime IDs are intentionally closed and path-free.
	FontFamily string `json:"font_family,omitempty" yaml:"font_family,omitempty"`
	// Bullet is the optional secondary typography of the bullet lane only.
	// Bullet-point copy uses this block, every other text lane keeps the
	// run-level typography; a field the block leaves unset falls back to the
	// run-level value, then to the planner's presentation default.
	Bullet *OverlayBulletStyleSpec `json:"bullet,omitempty" yaml:"bullet,omitempty"`
	// GlowSize and StrokeSize are pixel widths. Explicit zero disables the
	// corresponding effect; nil preserves the preset's value.
	GlowSize *float64 `json:"glow_size,omitempty" yaml:"glow_size,omitempty"`
	// GlowColor explicitly replaces a motion preset's glow color while keeping
	// its authored radius and intensity.
	GlowColor  string   `json:"glow_color,omitempty" yaml:"glow_color,omitempty"`
	StrokeSize *float64 `json:"stroke_size,omitempty" yaml:"stroke_size,omitempty"`
	// Cursor overrides the typewriter caret rendered alongside phrase
	// overlays: its chosen color, its pixel geometry (width = thickness,
	// height = pixel size) and its blink behaviour. Nil preserves the
	// motion-authored caret; explicit zero width hides it.
	Cursor *OverlayCursorStyleSpec `json:"cursor,omitempty" yaml:"cursor,omitempty"`
	// Image controls apply to generated image overlays and entity portraits.
	// They stay separate from text styling so one script can tune both layers.
	Image *OverlayImageStyleSpec `json:"image,omitempty" yaml:"image,omitempty"`
	// CardBackground is a per-text-layer plate. It is separate from the
	// full-canvas overlay_background, which would obscure source footage in a
	// final-job replacement window.
	CardBackground *OverlayCardBackgroundSpec `json:"card_background,omitempty" yaml:"card_background,omitempty"`
}

// OverlayBulletStyleSpec is the bullet-point typography override. It is the
// run-level counterpart of EditorialBulletStyle: one block styles every
// bullet in the run, while a per-scene editorial_bullet_styles pin still wins
// on the bullet it names.
type OverlayBulletStyleSpec struct {
	// FontFamily is one of SupportedFontFamilies, or empty for the
	// run-level/preset family.
	FontFamily string `json:"font_family,omitempty" yaml:"font_family,omitempty"`
	// FontSizePX is the bullet pixel size in [BulletFontSizeMinPX,
	// BulletFontSizeMaxPX]. Nil keeps the run-level size and then the size
	// the presentation pass derives from the bullet length.
	FontSizePX *float64 `json:"font_size_px,omitempty" yaml:"font_size_px,omitempty"`
	// GlowSize and StrokeSize are pixel widths; explicit zero disables the
	// effect. Nil falls back to the run-level value.
	GlowSize   *float64 `json:"glow_size,omitempty" yaml:"glow_size,omitempty"`
	GlowColor  string   `json:"glow_color,omitempty" yaml:"glow_color,omitempty"`
	StrokeSize *float64 `json:"stroke_size,omitempty" yaml:"stroke_size,omitempty"`
}

type OverlayCardBackgroundSpec struct {
	Color   string    `json:"color" yaml:"color"`
	Opacity *float64  `json:"opacity,omitempty" yaml:"opacity,omitempty"`
	Radius  *float64  `json:"radius,omitempty" yaml:"radius,omitempty"`
	Padding []float64 `json:"padding,omitempty" yaml:"padding,omitempty"`
	// Stroke draws the plate border. Width 0 disables it; nil keeps the
	// plate border-free, which is the historical behaviour.
	Stroke *OverlayCardBackgroundStrokeSpec `json:"stroke,omitempty" yaml:"stroke,omitempty"`
	// Shadow lifts the plate off the footage. The bounds mirror Chronon's
	// native drop-shadow ceiling so a declared blur is honoured exactly.
	Shadow *OverlayCardBackgroundShadowSpec `json:"shadow,omitempty" yaml:"shadow,omitempty"`
	// Motions is the plate's own entrance pool. Empty keeps the historical
	// behaviour: the plate copies the caption layer animation. A one-element
	// pool pins one motion; a longer pool rotates in plan order so one job can
	// show several plate entrances.
	Motions []string `json:"motions,omitempty" yaml:"motions,omitempty"`
}

// OverlayCardBackgroundStrokeSpec is the visible border of the text plate.
type OverlayCardBackgroundStrokeSpec struct {
	Color string  `json:"color" yaml:"color"`
	Width float64 `json:"width" yaml:"width"`
}

// OverlayCardBackgroundShadowSpec is the plate drop shadow. Empty fields use
// the renderer defaults (#000000, opacity 0.55, blur 12, offset [0, 6]).
type OverlayCardBackgroundShadowSpec struct {
	Color   string    `json:"color,omitempty" yaml:"color,omitempty"`
	Opacity *float64  `json:"opacity,omitempty" yaml:"opacity,omitempty"`
	Blur    *float64  `json:"blur,omitempty" yaml:"blur,omitempty"`
	Offset  []float64 `json:"offset,omitempty" yaml:"offset,omitempty"`
}

// CertifiedCardBackgroundMotions is the closed plate-entrance vocabulary the
// payload may select. It mirrors the renderer catalog in
// RenderingGen/renderinggen/internal/overlay/text_plate_motion.go; the two
// lists are pinned by a parity test because the modules are separate.
var CertifiedCardBackgroundMotions = []string{
	"fade",
	"slide_left",
	"slide_right",
	"slide_up",
	"scale_in",
	"wipe_right",
}

// IsCertifiedCardBackgroundMotion reports whether a plate motion is selectable.
func IsCertifiedCardBackgroundMotion(motion string) bool {
	for _, certified := range CertifiedCardBackgroundMotions {
		if motion == certified {
			return true
		}
	}
	return false
}

// OverlayCursorStyleSpec controls the runtime typewriter caret.
// Color is #RRGGBB; Width is the caret thickness in output pixels;
// Height is the caret pixel height; Blink opts into the post-type blink.
type OverlayCursorStyleSpec struct {
	Color  string   `json:"color,omitempty" yaml:"color,omitempty"`
	Width  *float64 `json:"width,omitempty" yaml:"width,omitempty"`
	Height *float64 `json:"height,omitempty" yaml:"height,omitempty"`
	Blink  *bool    `json:"blink,omitempty" yaml:"blink,omitempty"`
}

// MaxImageShadowBlurPX is the native separable drop-shadow ceiling Chronon
// certifies for an image card (drop shadow radius <= 64). Ingress validation
// and the projection share this one bound, so a declared blur is either
// honored exactly or rejected with an actionable error — never silently
// reduced to a value the payload did not ask for.
const MaxImageShadowBlurPX = 64.0

// ValidateImageStyle is the shared ingress gate for one image style block.
// The run-level overlay_style.image and a per-pin overlay_images[].style are
// the same published contract, so both are checked by the one owner of the
// bounds rather than a second copy that could drift.
func ValidateImageStyle(image *OverlayImageStyleSpec) error {
	if image == nil {
		return nil
	}
	return (&OverlayStyleSpec{Image: image}).Validate()
}

// OverlayImageStyleSpec controls image-card geometry and its visible frame.
// Geometry uses output pixels; position is relative to the canvas center.
// Bounded, when true, clips the card inside its authored box (fit controls
// how the pixels fill that box); nil preserves the preset behaviour.
type OverlayImageStyleSpec struct {
	Width     *int                `json:"width,omitempty" yaml:"width,omitempty"`
	Height    *int                `json:"height,omitempty" yaml:"height,omitempty"`
	PositionX *float64            `json:"position_x,omitempty" yaml:"position_x,omitempty"`
	PositionY *float64            `json:"position_y,omitempty" yaml:"position_y,omitempty"`
	Radius    *float64            `json:"radius,omitempty" yaml:"radius,omitempty"`
	Border    *OverlayImageBorder `json:"border,omitempty" yaml:"border,omitempty"`
	Stroke    *OverlayImageStroke `json:"stroke,omitempty" yaml:"stroke,omitempty"`
	Shadow    *OverlayShadowSpec  `json:"shadow,omitempty" yaml:"shadow,omitempty"`
	// Glow is the halo around the image card, lowered to Chronon's native
	// glow effect. Radius 0 disables it; color defaults to white and
	// intensity defaults to 0.25.
	Glow    *OverlayImageGlowSpec `json:"glow,omitempty" yaml:"glow,omitempty"`
	Bounded *bool                 `json:"bounded,omitempty" yaml:"bounded,omitempty"`
	Fit     string                `json:"fit,omitempty" yaml:"fit,omitempty"`
	// Effects decorates every image surface with the same bounded native
	// effect stack clip.render accepts, in declaration order. It is the
	// runtime "give the photo this treatment" control: the renderer lowers
	// each entry onto the photographic layer, never onto captions or plates.
	// `[{"type":"none"}]` explicitly clears any preset treatment.
	Effects []ImageEffectSpec `json:"effects,omitempty" yaml:"effects,omitempty"`
	// Grid draws a procedural screen-space grid for the image's lifetime. It
	// is the image counterpart of output.render.grid and needs no backdrop:
	// standalone images, composites and entity cards all honor it. Unset
	// values keep the renderer's verified defaults.
	Grid *OverlayImageGridSpec `json:"grid,omitempty" yaml:"grid,omitempty"`
	// Backdrop reuses a standalone image as a softly blurred full-canvas
	// background and can add one light canvas grid over it. Composite images
	// and groups keep their independent member frames, so the field is applied
	// only to a single-asset image item.
	Backdrop *OverlayImageBackdropSpec `json:"backdrop,omitempty" yaml:"backdrop,omitempty"`
}

// ImageEffectSpec is the image-surface spelling of the shared native effect
// contract. The struct is identical to the clip one on purpose: one closed
// catalog owns the type vocabulary, the accepted fields and their bounds, so
// an image stack and a clip stack can never drift apart.
type ImageEffectSpec = VideoClipEffectSpec

// MaxImageEffects is the renderer's per-image effect-stack ceiling. It pins
// the same bound clip.render publishes so one payload cannot ask for a stack
// the worker would refuse.
const MaxImageEffects = 12

// OverlayImageGridSpec is the image counterpart of output.render.grid, and
// the ONE grid contract for both a standalone image and its backdrop. Every
// field is optional: an omitted key keeps the renderer default (lines,
// #7FA8C8, 96 px spacing, 0.16 opacity), so a payload only names what it
// wants to change. Scope foreground bounds the grid to the scaled foreground
// area instead of the whole canvas; line and dot geometries are mutually
// exclusive.
type OverlayImageGridSpec struct {
	Enabled     *bool    `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Scope       string   `json:"scope,omitempty" yaml:"scope,omitempty"`
	Style       string   `json:"style,omitempty" yaml:"style,omitempty"`
	Color       string   `json:"color,omitempty" yaml:"color,omitempty"`
	SpacingPX   *float64 `json:"spacing_px,omitempty" yaml:"spacing_px,omitempty"`
	LineWidthPX *float64 `json:"line_width_px,omitempty" yaml:"line_width_px,omitempty"`
	DotRadiusPX *float64 `json:"dot_radius_px,omitempty" yaml:"dot_radius_px,omitempty"`
	Opacity     *float64 `json:"opacity,omitempty" yaml:"opacity,omitempty"`
}

// OverlayImageBackdropSpec mirrors params.image_backdrop on the RenderingGen
// side. Zoom is capped at 10x; blur must stay subtle. Grid reuses the ONE
// grid contract (OverlayImageGridSpec) instead of a twin type: the renderer
// publishes the same keys and the bounds are validated by the same function.
// Defaults are owned by the renderer so an omitted value keeps the verified
// editorial treatment.
type OverlayImageBackdropSpec struct {
	Zoom   *float64              `json:"zoom,omitempty" yaml:"zoom,omitempty"`
	BlurPX *float64              `json:"blur_px,omitempty" yaml:"blur_px,omitempty"`
	Grid   *OverlayImageGridSpec `json:"grid,omitempty" yaml:"grid,omitempty"`
}

type OverlayImageBorder struct {
	Width  float64  `json:"width" yaml:"width"`
	Color  string   `json:"color" yaml:"color"`
	Radius *float64 `json:"radius,omitempty" yaml:"radius,omitempty"`
}

type OverlayImageStroke struct {
	Width float64 `json:"width" yaml:"width"`
	Color string  `json:"color" yaml:"color"`
}

// OverlayImageGlowSpec controls the halo around a generated image card.
// Radius follows Chronon's native glow range [0,256] and 0 disables it;
// intensity is [0,4] and defaults to 0.25; color is #RRGGBB and defaults to
// #FFFFFF. The block is image-only and never styles caption text.
type OverlayImageGlowSpec struct {
	Radius    *float64 `json:"radius,omitempty" yaml:"radius,omitempty"`
	Intensity *float64 `json:"intensity,omitempty" yaml:"intensity,omitempty"`
	Color     string   `json:"color,omitempty" yaml:"color,omitempty"`
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
	if err := s.Bullet.validate(); err != nil {
		return err
	}
	if err := s.CardBackground.validate(); err != nil {
		return err
	}
	if s.GlowSize != nil && (math.IsNaN(*s.GlowSize) || math.IsInf(*s.GlowSize, 0) || *s.GlowSize < 0 || *s.GlowSize > 256) {
		return fmt.Errorf("overlay_style.glow_size must be between 0 and 256 pixels")
	}
	if s.GlowColor != "" && !color.IsHexRGB(s.GlowColor) {
		return fmt.Errorf("overlay_style.glow_color must be #RRGGBB")
	}
	if s.StrokeSize != nil && (math.IsNaN(*s.StrokeSize) || math.IsInf(*s.StrokeSize, 0) || *s.StrokeSize < 0 || *s.StrokeSize > 64) {
		return fmt.Errorf("overlay_style.stroke_size must be between 0 and 64 pixels")
	}
	if s.Size != nil && s.Size.FontSize != nil && (math.IsNaN(*s.Size.FontSize) || math.IsInf(*s.Size.FontSize, 0) || *s.Size.FontSize <= 0 || *s.Size.FontSize > 512) {
		return fmt.Errorf("overlay_style.size.font_size must be greater than 0 and at most 512 pixels")
	}
	if err := s.Cursor.validate(); err != nil {
		return err
	}
	if err := s.Image.validate(); err != nil {
		return err
	}
	return nil
}

// validate pins the Bullet block of OverlayStyleSpec. Nil-safe: an absent
// block has nothing to check.
func (bullet *OverlayBulletStyleSpec) validate() error {
	if bullet == nil {
		return nil
	}
	if !IsSupportedFontFamily(bullet.FontFamily) {
		return fmt.Errorf("overlay_style.bullet.font_family %q is unsupported (%s)", bullet.FontFamily, strings.Join(SupportedFontFamilies, ", "))
	}
	if bullet.FontSizePX != nil && (math.IsNaN(*bullet.FontSizePX) || math.IsInf(*bullet.FontSizePX, 0) ||
		*bullet.FontSizePX < BulletFontSizeMinPX || *bullet.FontSizePX > BulletFontSizeMaxPX) {
		return fmt.Errorf("overlay_style.bullet.font_size_px must be between %g and %g pixels", BulletFontSizeMinPX, BulletFontSizeMaxPX)
	}
	if bullet.GlowSize != nil && (math.IsNaN(*bullet.GlowSize) || math.IsInf(*bullet.GlowSize, 0) || *bullet.GlowSize < 0 || *bullet.GlowSize > 256) {
		return fmt.Errorf("overlay_style.bullet.glow_size must be between 0 and 256 pixels")
	}
	if bullet.GlowColor != "" && !color.IsHexRGB(bullet.GlowColor) {
		return fmt.Errorf("overlay_style.bullet.glow_color must be #RRGGBB")
	}
	if bullet.StrokeSize != nil && (math.IsNaN(*bullet.StrokeSize) || math.IsInf(*bullet.StrokeSize, 0) || *bullet.StrokeSize < 0 || *bullet.StrokeSize > 64) {
		return fmt.Errorf("overlay_style.bullet.stroke_size must be between 0 and 64 pixels")
	}
	return nil
}

// validate pins the CardBackground block of OverlayStyleSpec. Nil-safe: an absent
// block has nothing to check.
func (bg *OverlayCardBackgroundSpec) validate() error {
	if bg == nil {
		return nil
	}
	if !color.IsHexRGB(bg.Color) {
		return fmt.Errorf("overlay_style.card_background.color must be #RRGGBB")
	}
	if bg.Opacity != nil && (math.IsNaN(*bg.Opacity) || math.IsInf(*bg.Opacity, 0) || *bg.Opacity < 0 || *bg.Opacity > 1) {
		return fmt.Errorf("overlay_style.card_background.opacity must be between 0 and 1")
	}
	if bg.Radius != nil && (math.IsNaN(*bg.Radius) || math.IsInf(*bg.Radius, 0) || *bg.Radius < 0 || *bg.Radius > 512) {
		return fmt.Errorf("overlay_style.card_background.radius must be between 0 and 512 pixels")
	}
	if len(bg.Padding) > 2 {
		return fmt.Errorf("overlay_style.card_background.padding accepts at most [x,y]")
	}
	for _, value := range bg.Padding {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 512 {
			return fmt.Errorf("overlay_style.card_background.padding values must be between 0 and 512 pixels")
		}
	}
	if err := bg.validateStroke(); err != nil {
		return err
	}
	if err := bg.validateShadow(); err != nil {
		return err
	}
	return bg.validateMotions()
}

// validateStroke pins the card-background stroke block. Nil-safe.
func (bg *OverlayCardBackgroundSpec) validateStroke() error {
	if bg == nil {
		return nil
	}
	stroke := bg.Stroke
	if stroke == nil {
		return nil
	}
	if !color.IsHexRGB(stroke.Color) {
		return fmt.Errorf("overlay_style.card_background.stroke.color must be #RRGGBB")
	}
	if math.IsNaN(stroke.Width) || math.IsInf(stroke.Width, 0) || stroke.Width < 0 || stroke.Width > 64 {
		return fmt.Errorf("overlay_style.card_background.stroke.width must be between 0 and 64 pixels")
	}
	return nil
}

// validateShadow pins the card-background shadow block. Nil-safe.
func (bg *OverlayCardBackgroundSpec) validateShadow() error {
	if bg == nil {
		return nil
	}
	shadow := bg.Shadow
	if shadow == nil {
		return nil
	}
	if shadow.Color != "" && !color.IsHexRGB(shadow.Color) {
		return fmt.Errorf("overlay_style.card_background.shadow.color must be #RRGGBB")
	}
	if shadow.Opacity != nil && (math.IsNaN(*shadow.Opacity) || math.IsInf(*shadow.Opacity, 0) || *shadow.Opacity < 0 || *shadow.Opacity > 1) {
		return fmt.Errorf("overlay_style.card_background.shadow.opacity must be between 0 and 1")
	}
	if shadow.Blur != nil && (math.IsNaN(*shadow.Blur) || math.IsInf(*shadow.Blur, 0) || *shadow.Blur < 0 || *shadow.Blur > MaxImageShadowBlurPX) {
		return fmt.Errorf("overlay_style.card_background.shadow.blur must be between 0 and %g pixels", MaxImageShadowBlurPX)
	}
	if len(shadow.Offset) != 0 && len(shadow.Offset) != 2 {
		return fmt.Errorf("overlay_style.card_background.shadow.offset requires [x,y]")
	}
	for _, value := range shadow.Offset {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < -128 || value > 128 {
			return fmt.Errorf("overlay_style.card_background.shadow.offset values must be between -128 and 128 pixels")
		}
	}
	return nil
}

// validateMotions pins the certified-motion ceiling and allowlist. Nil-safe.
func (bg *OverlayCardBackgroundSpec) validateMotions() error {
	if bg == nil {
		return nil
	}
	if len(bg.Motions) > 8 {
		return fmt.Errorf("overlay_style.card_background.motions accepts at most 8 entries")
	}
	for _, motion := range bg.Motions {
		if !IsCertifiedCardBackgroundMotion(motion) {
			return fmt.Errorf("overlay_style.card_background.motions %q is unsupported (%s)", motion, strings.Join(CertifiedCardBackgroundMotions, ", "))
		}
	}
	return nil
}

// validate pins the Cursor block of OverlayStyleSpec. Nil-safe: an absent
// block has nothing to check.
func (cursor *OverlayCursorStyleSpec) validate() error {
	if cursor == nil {
		return nil
	}
	if cursor.Color != "" && !color.IsHexRGB(cursor.Color) {
		return fmt.Errorf("overlay_style.cursor.color must be #RRGGBB")
	}
	if cursor.Width != nil && (math.IsNaN(*cursor.Width) || math.IsInf(*cursor.Width, 0) || *cursor.Width < 0 || *cursor.Width > 64) {
		return fmt.Errorf("overlay_style.cursor.width must be between 0 and 64 pixels")
	}
	if cursor.Height != nil && (math.IsNaN(*cursor.Height) || math.IsInf(*cursor.Height, 0) || *cursor.Height < 0 || *cursor.Height > 256) {
		return fmt.Errorf("overlay_style.cursor.height must be between 0 and 256 pixels")
	}
	return nil
}

// validate pins the Image block of OverlayStyleSpec. Nil-safe: an absent
// block has nothing to check.
func (image *OverlayImageStyleSpec) validate() error {
	if image == nil {
		return nil
	}
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
	if err := image.validateFrame(); err != nil {
		return err
	}
	if err := image.validateShadow(); err != nil {
		return err
	}
	if err := image.validateGlow(); err != nil {
		return err
	}
	if err := validateImageEffects(image.Effects); err != nil {
		return err
	}
	if err := validateImageGrid("overlay_style.image.grid", image.Grid); err != nil {
		return err
	}
	if err := image.validateBackdrop(); err != nil {
		return err
	}
	return nil
}

// validateFrame pins the border and stroke blocks. Nil-safe: an absent block
// has nothing to check.
func (image *OverlayImageStyleSpec) validateFrame() error {
	if image == nil {
		return nil
	}
	if border := image.Border; border != nil {
		if math.IsNaN(border.Width) || math.IsInf(border.Width, 0) || border.Width < 0 || border.Width > 512 {
			return fmt.Errorf("overlay_style.image.border.width must be between 0 and 512 pixels")
		}
		if border.Width > 0 && !color.IsHexRGB(border.Color) {
			return fmt.Errorf("overlay_style.image.border.color must be #RRGGBB when border width is positive")
		}
		if border.Radius != nil && (math.IsNaN(*border.Radius) || math.IsInf(*border.Radius, 0) || *border.Radius < 0 || *border.Radius > 512) {
			return fmt.Errorf("overlay_style.image.border.radius must be between 0 and 512 pixels")
		}
	}
	if stroke := image.Stroke; stroke != nil {
		if math.IsNaN(stroke.Width) || math.IsInf(stroke.Width, 0) || stroke.Width < 0 || stroke.Width > 64 {
			return fmt.Errorf("overlay_style.image.stroke.width must be between 0 and 64 pixels")
		}
		if stroke.Width > 0 && !color.IsHexRGB(stroke.Color) {
			return fmt.Errorf("overlay_style.image.stroke.color must be #RRGGBB when stroke width is positive")
		}
	}
	return nil
}

// validateShadow pins the shadow block. Nil-safe: an absent block has nothing
// to check.
func (image *OverlayImageStyleSpec) validateShadow() error {
	if image == nil {
		return nil
	}
	shadow := image.Shadow
	if shadow == nil {
		return nil
	}
	if shadow.Opacity != nil && (math.IsNaN(*shadow.Opacity) || math.IsInf(*shadow.Opacity, 0) || *shadow.Opacity < 0 || *shadow.Opacity > 1) {
		return fmt.Errorf("overlay_style.image.shadow.opacity must be between 0 and 1")
	}
	if shadow.Blur != nil && (math.IsNaN(*shadow.Blur) || math.IsInf(*shadow.Blur, 0) || *shadow.Blur < 0 || *shadow.Blur > MaxImageShadowBlurPX) {
		return fmt.Errorf("overlay_style.image.shadow.blur must be between 0 and %g pixels", MaxImageShadowBlurPX)
	}
	if len(shadow.Offset) > 2 {
		return fmt.Errorf("overlay_style.image.shadow.offset accepts at most [x,y]")
	}
	for _, offset := range shadow.Offset {
		if math.IsNaN(offset) || math.IsInf(offset, 0) || math.Abs(offset) > 256 {
			return fmt.Errorf("overlay_style.image.shadow.offset values must be within ±256 pixels")
		}
	}
	if shadow.Color != "" && !color.IsHexRGB(shadow.Color) {
		return fmt.Errorf("overlay_style.image.shadow.color must be #RRGGBB")
	}
	return nil
}

// validateGlow pins the glow block. Nil-safe: an absent block has nothing to
// check.
func (image *OverlayImageStyleSpec) validateGlow() error {
	if image == nil {
		return nil
	}
	glow := image.Glow
	if glow == nil {
		return nil
	}
	if glow.Radius != nil && (math.IsNaN(*glow.Radius) || math.IsInf(*glow.Radius, 0) || *glow.Radius < 0 || *glow.Radius > 256) {
		return fmt.Errorf("overlay_style.image.glow.radius must be between 0 and 256 pixels")
	}
	if glow.Intensity != nil && (math.IsNaN(*glow.Intensity) || math.IsInf(*glow.Intensity, 0) || *glow.Intensity < 0 || *glow.Intensity > 4) {
		return fmt.Errorf("overlay_style.image.glow.intensity must be between 0 and 4")
	}
	if glow.Color != "" && !color.IsHexRGB(glow.Color) {
		return fmt.Errorf("overlay_style.image.glow.color must be #RRGGBB")
	}
	return nil
}

// validateBackdrop pins the backdrop block. Nil-safe: an absent block has
// nothing to check.
func (image *OverlayImageStyleSpec) validateBackdrop() error {
	if image == nil {
		return nil
	}
	backdrop := image.Backdrop
	if backdrop == nil {
		return nil
	}
	if backdrop.Zoom != nil && (math.IsNaN(*backdrop.Zoom) || math.IsInf(*backdrop.Zoom, 0) || *backdrop.Zoom < 1 || *backdrop.Zoom > 10) {
		return fmt.Errorf("overlay_style.image.backdrop.zoom must be between 1 and 10")
	}
	if backdrop.BlurPX != nil && (math.IsNaN(*backdrop.BlurPX) || math.IsInf(*backdrop.BlurPX, 0) || *backdrop.BlurPX < 6 || *backdrop.BlurPX > 48) {
		return fmt.Errorf("overlay_style.image.backdrop.blur_px must be between 6 and 48 pixels")
	}
	if err := validateImageGrid("overlay_style.image.backdrop.grid", backdrop.Grid); err != nil {
		return err
	}
	return nil
}

// validateImageEffects pins the structural half of the shared effect contract
// at ingress: count ceiling, required type, finite numbers and the "none"
// sentinel. Per-type parameter bounds are owned by the single cliprender
// catalog and re-checked by the renderer, so they are never copied here.
func validateImageEffects(effects []ImageEffectSpec) error {
	if len(effects) > MaxImageEffects {
		return fmt.Errorf("overlay_style.image.effects cannot exceed %d entries", MaxImageEffects)
	}
	for index, effect := range effects {
		prefix := fmt.Sprintf("overlay_style.image.effects[%d]", index)
		if strings.TrimSpace(effect.Type) == "" {
			return fmt.Errorf("%s.type is required", prefix)
		}
		for name, value := range map[string]float64{
			"amount": effect.Amount, "radius": effect.Radius, "softness": effect.Softness,
			"angle": effect.Angle, "intensity": effect.Intensity, "amplitude": effect.Amplitude,
			"wavelength": effect.Wavelength, "curvature": effect.Curvature, "scale": effect.Scale,
			"threshold": effect.Threshold, "speed": effect.Speed,
		} {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("%s.%s must be a finite number", prefix, name)
			}
		}
		if effect.Type != "none" {
			continue
		}
		if len(effects) != 1 {
			return fmt.Errorf("%s.type none cannot be combined with other effects", prefix)
		}
		if effect.Amount != 0 || effect.Radius != 0 || effect.Softness != 0 || effect.BlockSize != 0 || effect.Size != 0 ||
			effect.Angle != 0 || effect.Intensity != 0 || effect.Amplitude != 0 || effect.Wavelength != 0 || effect.Seed != 0 ||
			effect.Animated || effect.Curvature != 0 || effect.Scale != 0 || effect.Threshold != 0 || effect.Speed != 0 {
			return fmt.Errorf("%s.type none accepts no parameters", prefix)
		}
	}
	return nil
}

// validateImageGrid mirrors the renderer's published grid bounds for BOTH
// grid sites (image.grid and image.backdrop.grid): the shape is one contract,
// so only the error prefix differs. Missing keys keep the verified defaults,
// so only supplied values are checked; the line/dot geometries stay mutually
// exclusive.
func validateImageGrid(prefix string, grid *OverlayImageGridSpec) error {
	if grid == nil {
		return nil
	}
	if grid.Scope != "" && grid.Scope != "full_canvas" && grid.Scope != "foreground" {
		return fmt.Errorf("%s.scope must be full_canvas or foreground", prefix)
	}
	if grid.Style != "" && grid.Style != "lines" && grid.Style != "line" && grid.Style != "grid" && grid.Style != "dots" && grid.Style != "dot_grid" {
		return fmt.Errorf("%s.style must be lines or dots", prefix)
	}
	if grid.Color != "" && !color.IsHexRGB(grid.Color) {
		return fmt.Errorf("%s.color must be #RRGGBB", prefix)
	}
	if grid.SpacingPX != nil && (math.IsNaN(*grid.SpacingPX) || math.IsInf(*grid.SpacingPX, 0) || *grid.SpacingPX < 4 || *grid.SpacingPX > 512) {
		return fmt.Errorf("%s.spacing_px must be between 4 and 512 pixels", prefix)
	}
	if grid.LineWidthPX != nil && (math.IsNaN(*grid.LineWidthPX) || math.IsInf(*grid.LineWidthPX, 0) || *grid.LineWidthPX < 0.25 || *grid.LineWidthPX > 32) {
		return fmt.Errorf("%s.line_width_px must be between 0.25 and 32 pixels", prefix)
	}
	if grid.DotRadiusPX != nil && (math.IsNaN(*grid.DotRadiusPX) || math.IsInf(*grid.DotRadiusPX, 0) || *grid.DotRadiusPX < 0.5 || *grid.DotRadiusPX > 64) {
		return fmt.Errorf("%s.dot_radius_px must be between 0.5 and 64 pixels", prefix)
	}
	if grid.Opacity != nil && (math.IsNaN(*grid.Opacity) || math.IsInf(*grid.Opacity, 0) || *grid.Opacity < 0 || *grid.Opacity > 1) {
		return fmt.Errorf("%s.opacity must be between 0 and 1", prefix)
	}
	isDots := grid.Style == "dots" || grid.Style == "dot_grid"
	if isDots && grid.LineWidthPX != nil {
		return fmt.Errorf("%s.line_width_px is valid only for line grids", prefix)
	}
	if !isDots && grid.DotRadiusPX != nil {
		return fmt.Errorf("%s.dot_radius_px is valid only for dot grids", prefix)
	}
	return nil
}

type OverlayShadowSpec struct {
	Enabled bool      `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Color   string    `json:"color,omitempty" yaml:"color,omitempty"`
	Opacity *float64  `json:"opacity,omitempty" yaml:"opacity,omitempty"`
	Blur    *float64  `json:"blur,omitempty" yaml:"blur,omitempty"`
	Offset  []float64 `json:"offset,omitempty" yaml:"offset,omitempty"`
}

type OverlaySizeSpec struct {
	Width    *int     `json:"width,omitempty" yaml:"width,omitempty"`
	Height   *int     `json:"height,omitempty" yaml:"height,omitempty"`
	FontSize *float64 `json:"font_size,omitempty" yaml:"font_size,omitempty"`
}

type OverlayTransitionSpec struct {
	Preset         string `json:"preset" yaml:"preset"`
	DurationFrames int    `json:"duration_frames,omitempty" yaml:"duration_frames,omitempty"`
}
