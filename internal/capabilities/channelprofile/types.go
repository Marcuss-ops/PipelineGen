// Package channelprofile owns the per-channel editorial profile: the durable,
// operator-editable defaults a channel gets for the render surfaces a caller
// left BLANK (subtitles, watermark, overlay style, SFX, mix policy, phrase
// motion pool).
//
// Ownership split (mirrors how ApplyEditingAssetPolicy is governed):
//
//   - The CALLER owns every explicit value. A profile never overwrites a
//     selection the request already carries — it only completes blanks.
//   - The PROFILE owns curated channel defaults. It is data loaded once at
//     composition time (config/channel_profiles.yaml), so changing a channel's
//     look is an edit + restart, not a code change and not a RenderingGen
//     redeploy.
//   - RenderingGen still NEVER invents style: everything below projects into
//     the typed fields PipelineGen already emits on renderinggen.overlay-plan.v1.
//
// The nested structs are deliberate field-for-field mirrors of the kernel
// types they project onto (VideoVisualStyleSpec, OverlayStyleSpec,
// SoundEffectIntent), because the kernel types carry JSON tags only and this
// file is the YAML boundary. Keep a mirror in sync when the kernel block
// grows — exactly the rule visual_style_resolver.go states for styleBlock.
package channelprofile

import (
	"encoding/json"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Profile is one channel's curated defaults. Every block is optional; a
// profile may carry any subset and applies only the blocks it declares.
type Profile struct {
	// ChannelID is the lookup key: the canonical YouTube channel id or any
	// operator-defined channel key. Required and unique within the document.
	ChannelID string `yaml:"channel_id"`
	// Description is operator documentation only; it never reaches a render.
	Description string `yaml:"description,omitempty"`

	// Subtitles fills output.render.subtitles when the caller declared none.
	Subtitles *SubtitlesProfile `yaml:"subtitles,omitempty"`
	// Watermark fills output.render.watermark when the caller declared none.
	// Text watermark only: an image/logo watermark needs asset plumbing that
	// this profile deliberately does not own.
	Watermark *WatermarkProfile `yaml:"watermark,omitempty"`
	// OverlayStyle fills the request-level overlay_style (phrase/word overlay
	// color, size, shadow, entry animation) when the caller declared none.
	OverlayStyle *OverlayStyleProfile `yaml:"overlay_style,omitempty"`
	// SoundEffects fills the request's SFX intent list when it is empty.
	// The intents flow through the canonical pipeline
	// (AudioAssetResolver → AudioIntentResolver → CompileWithLayersAndPolicy)
	// into the master mix; this profile only chooses WHICH effects, WHERE.
	SoundEffects []SoundEffectProfile `yaml:"sound_effects,omitempty"`
	// MixPolicy fills audio.mix_policy when the caller declared none.
	// Validated against the canonical AudioMixPolicy vocabulary at load.
	MixPolicy string `yaml:"mix_policy,omitempty"`
	// PhraseMotions replaces the certified phrase-motion rotation pool for the
	// run (the animated IMPORTANT_PHRASE overlays). Empty means "keep the
	// certified default pool"; every id must be a certified motion.
	PhraseMotions []string `yaml:"phrase_motions,omitempty"`
	// ImageMotions is retained for payload compatibility. Non-empty values are
	// rejected because generated images currently use certified 2D presets.
	ImageMotions []string `yaml:"image_motions,omitempty"`
}

// SubtitlesProfile is the channel's subtitle choice. Presence of the block
// means "this channel wants subtitles"; Enabled:false lets a profile
// explicitly stay out of the way instead of being silently inert.
type SubtitlesProfile struct {
	// Enabled defaults to true when the block exists. false records an
	// explicit "no channel subtitles", which the applier honours by leaving
	// the request untouched.
	Enabled *bool `yaml:"enabled,omitempty"`
	// Mode is "burn" (default) or "sidecar", mirroring overlay-plan.v1.
	Mode string `yaml:"mode,omitempty"`
	// Preset is a CANONICAL subtitle preset id (kernel canonicalSubtitlePresets).
	// Request-local presets are deliberately not addressable from a profile.
	Preset string `yaml:"preset,omitempty"`
	// Style is the inline override merged OVER the preset (explicit wins).
	Style *StyleProfile `yaml:"style,omitempty"`
}

// StyleProfile mirrors scriptpkg.VideoVisualStyleSpec (the same wire block
// PipelineGen's VideoVisualStyleSpec projects onto renderinggen.overlay-plan.v1
// subtitles.style / watermark.style).
type StyleProfile struct {
	Font         string         `yaml:"font,omitempty"`
	Position     string         `yaml:"position,omitempty"`
	Size         float64        `yaml:"size,omitempty"`
	Color        string         `yaml:"color,omitempty"`
	FontSizePX   float64        `yaml:"font_size_px,omitempty"`
	WidthPX      int            `yaml:"width_px,omitempty"`
	HeightPX     int            `yaml:"height_px,omitempty"`
	ScalePercent float64        `yaml:"scale_percent,omitempty"`
	Stroke       *StrokeProfile `yaml:"stroke,omitempty"`
	Shadow       *ShadowProfile `yaml:"shadow,omitempty"`
}

// StrokeProfile mirrors scriptpkg.VideoStrokeSpec.
type StrokeProfile struct {
	Color string  `yaml:"color,omitempty"`
	Width float64 `yaml:"width,omitempty"`
}

// ShadowProfile mirrors scriptpkg.VideoShadowSpec (offset_x/offset_y
// spelling, the same one the RenderingGen style resolver accepts).
type ShadowProfile struct {
	Color   string  `yaml:"color,omitempty"`
	Opacity float64 `yaml:"opacity,omitempty"`
	BlurPX  float64 `yaml:"blur_px,omitempty"`
	OffsetX float64 `yaml:"offset_x,omitempty"`
	OffsetY float64 `yaml:"offset_y,omitempty"`
}

// VisualStyle projects the mirror onto the kernel type.
func (s *StyleProfile) VisualStyle() *scriptpkg.VideoVisualStyleSpec {
	if s == nil {
		return nil
	}
	out := &scriptpkg.VideoVisualStyleSpec{
		Font:         s.Font,
		Position:     s.Position,
		Size:         s.Size,
		Color:        s.Color,
		FontSizePX:   s.FontSizePX,
		WidthPX:      s.WidthPX,
		HeightPX:     s.HeightPX,
		ScalePercent: s.ScalePercent,
	}
	if s.Stroke != nil {
		out.Stroke = &scriptpkg.VideoStrokeSpec{Color: s.Stroke.Color, Width: s.Stroke.Width}
	}
	if s.Shadow != nil {
		out.Shadow = &scriptpkg.VideoShadowSpec{
			Color: s.Shadow.Color, Opacity: s.Shadow.Opacity,
			BlurPX: s.Shadow.BlurPX, OffsetX: s.Shadow.OffsetX, OffsetY: s.Shadow.OffsetY,
		}
	}
	return out
}

// WatermarkProfile mirrors scriptpkg.VideoWatermarkSpec minus the pieces a
// profile must not own (AssetID: logo watermarks need asset plumbing).
type WatermarkProfile struct {
	Text     string        `yaml:"text"`
	Position string        `yaml:"position,omitempty"`
	Opacity  float64       `yaml:"opacity,omitempty"`
	MarginPX int           `yaml:"margin_px,omitempty"`
	Style    *StyleProfile `yaml:"style,omitempty"`
}

// Watermark projects the mirror onto the kernel type. Enabled is always true:
// a profile that declares a watermark wants it.
func (w *WatermarkProfile) Watermark() *scriptpkg.VideoWatermarkSpec {
	if w == nil {
		return nil
	}
	return &scriptpkg.VideoWatermarkSpec{
		Enabled:  true,
		Text:     w.Text,
		Position: w.Position,
		Opacity:  w.Opacity,
		MarginPX: w.MarginPX,
		Style:    w.Style.VisualStyle(),
	}
}

// OverlayStyleProfile mirrors scriptpkg.OverlayStyleSpec.
type OverlayStyleProfile struct {
	Color        []float64                 `yaml:"color,omitempty"`
	Size         *OverlaySizeProfile       `yaml:"size,omitempty"`
	Shadow       *OverlayShadowProfile     `yaml:"shadow,omitempty"`
	TransitionIn *OverlayTransitionProfile `yaml:"transition_in,omitempty"`
	FontFamily   string                    `yaml:"font_family,omitempty"`
	GlowSize     *float64                  `yaml:"glow_size,omitempty"`
	StrokeSize   *float64                  `yaml:"stroke_size,omitempty"`
}

// OverlaySizeProfile mirrors scriptpkg.OverlaySizeSpec.
type OverlaySizeProfile struct {
	Width    *int     `yaml:"width,omitempty"`
	Height   *int     `yaml:"height,omitempty"`
	FontSize *float64 `yaml:"font_size,omitempty"`
}

// OverlayShadowProfile mirrors scriptpkg.OverlayShadowSpec.
type OverlayShadowProfile struct {
	Enabled *bool     `yaml:"enabled,omitempty"`
	Color   string    `yaml:"color,omitempty"`
	Opacity *float64  `yaml:"opacity,omitempty"`
	Blur    *float64  `yaml:"blur,omitempty"`
	Offset  []float64 `yaml:"offset,omitempty"`
}

// OverlayTransitionProfile mirrors scriptpkg.OverlayTransitionSpec.
type OverlayTransitionProfile struct {
	Preset         string `yaml:"preset"`
	DurationFrames int    `yaml:"duration_frames,omitempty"`
}

// OverlayStyle projects the mirror onto the kernel type.
func (o *OverlayStyleProfile) OverlayStyle() *scriptpkg.OverlayStyleSpec {
	if o == nil {
		return nil
	}
	out := &scriptpkg.OverlayStyleSpec{
		Color:      append([]float64(nil), o.Color...),
		FontFamily: o.FontFamily, GlowSize: o.GlowSize, StrokeSize: o.StrokeSize,
	}
	if o.Size != nil {
		size := &scriptpkg.OverlaySizeSpec{
			Width: o.Size.Width, Height: o.Size.Height, FontSize: o.Size.FontSize,
		}
		out.Size = size
	}
	if o.Shadow != nil {
		shadow := &scriptpkg.OverlayShadowSpec{
			Color:  o.Shadow.Color,
			Offset: append([]float64(nil), o.Shadow.Offset...),
		}
		if o.Shadow.Enabled != nil {
			shadow.Enabled = *o.Shadow.Enabled
		} else {
			// A shadow block that carries tuning is enabled by intent; an
			// explicitly disabled block says so.
			shadow.Enabled = true
		}
		shadow.Opacity = o.Shadow.Opacity
		shadow.Blur = o.Shadow.Blur
		out.Shadow = shadow
	}
	if o.TransitionIn != nil {
		out.TransitionIn = &scriptpkg.OverlayTransitionSpec{
			Preset: o.TransitionIn.Preset, DurationFrames: o.TransitionIn.DurationFrames,
		}
	}
	return out
}

// SoundEffectProfile mirrors scriptpkg.SoundEffectIntent's wire keys
// (at_ms / scene_id / anchor / offset_ms / source_in_ms / duration_ms /
// gain_db). GainDB is a POINTER so "absent" stays distinguishable from an
// explicit 0 dB: the projection round-trips through JSON so the intent's own
// UnmarshalJSON applies the canonical built-in one-shot default when the key
// is absent, exactly as it does for a caller payload.
type SoundEffectProfile struct {
	AssetID    string   `yaml:"asset_id"`
	AtMS       int64    `yaml:"at_ms,omitempty"`
	SceneID    string   `yaml:"scene_id,omitempty"`
	Anchor     string   `yaml:"anchor,omitempty"`
	OffsetMS   int64    `yaml:"offset_ms,omitempty"`
	SourceInMS int64    `yaml:"source_in_ms,omitempty"`
	DurationMS int64    `yaml:"duration_ms,omitempty"`
	GainDB     *float64 `yaml:"gain_db,omitempty"`
}

// soundEffect projects the mirror onto the kernel intent, reusing the
// intent's own JSON decoding so profile-authored effects and caller-authored
// effects resolve identically (same gain defaulting, same anchor rule).
func (s SoundEffectProfile) soundEffect() (scriptpkg.SoundEffectIntent, error) {
	raw := map[string]any{"asset_id": strings.TrimSpace(s.AssetID)}
	if s.AtMS != 0 {
		raw["at_ms"] = s.AtMS
	}
	if s.SceneID != "" {
		raw["scene_id"] = s.SceneID
	}
	if s.Anchor != "" {
		raw["anchor"] = s.Anchor
	}
	if s.OffsetMS != 0 {
		raw["offset_ms"] = s.OffsetMS
	}
	if s.SourceInMS != 0 {
		raw["source_in_ms"] = s.SourceInMS
	}
	if s.DurationMS != 0 {
		raw["duration_ms"] = s.DurationMS
	}
	if s.GainDB != nil {
		raw["gain_db"] = *s.GainDB
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return scriptpkg.SoundEffectIntent{}, fmt.Errorf("channelprofile: encode sfx %q: %w", s.AssetID, err)
	}
	var intent scriptpkg.SoundEffectIntent
	if err := json.Unmarshal(data, &intent); err != nil {
		return scriptpkg.SoundEffectIntent{}, fmt.Errorf("channelprofile: decode sfx %q: %w", s.AssetID, err)
	}
	return intent, nil
}

// SoundEffectsIntents projects every declared effect in declaration order.
// It is exported because the applier (scriptgeneration.ApplyChannelProfile)
// needs the projected intents, while the projection itself belongs here.
func (p Profile) SoundEffectsIntents() ([]scriptpkg.SoundEffectIntent, error) {
	if len(p.SoundEffects) == 0 {
		return nil, nil
	}
	out := make([]scriptpkg.SoundEffectIntent, 0, len(p.SoundEffects))
	for _, s := range p.SoundEffects {
		intent, err := s.soundEffect()
		if err != nil {
			return nil, err
		}
		out = append(out, intent)
	}
	return out, nil
}
