package channelprofile

import (
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SubtitlePositions and WatermarkPositions are the MIRRORS of the position
// vocabularies RenderingGen's visual style resolver accepts
// (subtitleCueGeometry: bottom_center/top_center/middle_center;
// resolveWatermarkGeometry: top_left/top_right/center/bottom_left/bottom_right).
// They exist so a channel profile that names an impossible position is
// rejected at LOAD time with a readable error instead of failing some job's
// compile later. When RenderingGen widens its vocabulary, widen these two sets
// in the same change.
var (
	subtitlePositions  = map[string]bool{"bottom_center": true, "top_center": true, "middle_center": true}
	watermarkPositions = map[string]bool{
		"top_left": true, "top_right": true, "center": true,
		"bottom_left": true, "bottom_right": true,
	}
)

// Validate fails closed on every profile fact this build cannot honour. It
// runs at load time, before anything is installed, so the registry only ever
// holds profiles that are executable.
func (p Profile) Validate() error {
	if strings.TrimSpace(p.ChannelID) == "" {
		return fmt.Errorf("channelprofile: a profile without channel_id cannot be looked up")
	}
	if err := p.validateSubtitles(); err != nil {
		return fmt.Errorf("channelprofile %q: %w", p.ChannelID, err)
	}
	if err := p.validateWatermark(); err != nil {
		return fmt.Errorf("channelprofile %q: %w", p.ChannelID, err)
	}
	if err := p.validateOverlayStyle(); err != nil {
		return fmt.Errorf("channelprofile %q: %w", p.ChannelID, err)
	}
	if err := p.validateSoundEffects(); err != nil {
		return fmt.Errorf("channelprofile %q: %w", p.ChannelID, err)
	}
	if err := p.validateMixPolicy(); err != nil {
		return fmt.Errorf("channelprofile %q: %w", p.ChannelID, err)
	}
	if err := p.validatePhraseMotions(); err != nil {
		return fmt.Errorf("channelprofile %q: %w", p.ChannelID, err)
	}
	if err := p.validateImageMotions(); err != nil {
		return fmt.Errorf("channelprofile %q: %w", p.ChannelID, err)
	}
	return nil
}

func (p Profile) validateSubtitles() error {
	s := p.Subtitles
	if s == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(s.Mode)) {
	case "", "burn", "sidecar":
	default:
		return fmt.Errorf("subtitles.mode %q is not a renderinggen.overlay-plan.v1 mode (burn, sidecar)", s.Mode)
	}
	if preset := strings.TrimSpace(s.Preset); preset != "" && !scriptpkg.IsValidSubtitlePreset(preset) {
		return fmt.Errorf("subtitles.preset %q is not a canonical subtitle preset this build ships", preset)
	}
	if strings.TrimSpace(s.Preset) == "" && !hasExplicitFontSize(s.Style) {
		// RenderingGen's visual style resolver fails closed when a burn-mode
		// subtitle style carries no size (the worker never invents typography),
		// and unlike the watermark Normalize does not default a subtitle font
		// size. Reject at load instead of at some job's compile.
		return fmt.Errorf("subtitles requires a preset or style.font_size_px (the renderer never invents a font size)")
	}
	if st := s.Style; st != nil {
		if pos := strings.TrimSpace(st.Position); pos != "" && !subtitlePositions[pos] {
			return fmt.Errorf("subtitles.style.position %q is unsupported (bottom_center, top_center, middle_center)", pos)
		}
		if st.FontSizePX < 0 || st.Size < 0 {
			return fmt.Errorf("subtitles.style carries a negative font size")
		}
	}
	return nil
}

// hasExplicitFontSize reports whether a style block carries a usable size —
// the one value the lowering refuses to invent (effectiveFontSize fails
// closed in RenderingGen's visual style resolver).
func hasExplicitFontSize(s *StyleProfile) bool {
	if s == nil {
		return false
	}
	return s.FontSizePX > 0 || s.Size > 0
}

func (p Profile) validateWatermark() error {
	w := p.Watermark
	if w == nil {
		return nil
	}
	if strings.TrimSpace(w.Text) == "" {
		// A watermark block without text would be injected as an empty layer
		// and fail closed at render — reject it where the operator can see it.
		return fmt.Errorf("watermark.text is required")
	}
	if pos := strings.TrimSpace(w.Position); pos != "" && !watermarkPositions[pos] {
		return fmt.Errorf("watermark.position %q is unsupported (top_left, top_right, center, bottom_left, bottom_right)", pos)
	}
	if w.MarginPX < 0 {
		return fmt.Errorf("watermark.margin_px is negative")
	}
	if w.Opacity < 0 || w.Opacity > 1 {
		return fmt.Errorf("watermark.opacity %g is outside [0,1]", w.Opacity)
	}
	if st := w.Style; st != nil && (st.FontSizePX < 0 || st.Size < 0) {
		return fmt.Errorf("watermark.style carries a negative font size")
	}
	return nil
}

func (p Profile) validateOverlayStyle() error {
	o := p.OverlayStyle
	if o == nil {
		return nil
	}
	if len(o.Color) != 0 && len(o.Color) != 4 {
		return fmt.Errorf("overlay_style.color must be an RGBA[4] of 0..1 components")
	}
	for i, c := range o.Color {
		if c < 0 || c > 1 {
			return fmt.Errorf("overlay_style.color[%d]=%g is outside 0..1", i, c)
		}
	}
	if o.Shadow != nil && len(o.Shadow.Offset) != 0 && len(o.Shadow.Offset) != 2 {
		return fmt.Errorf("overlay_style.shadow.offset must be [x,y]")
	}
	if o.TransitionIn != nil && strings.TrimSpace(o.TransitionIn.Preset) == "" {
		return fmt.Errorf("overlay_style.transition_in requires a preset")
	}
	if o.TransitionIn != nil && o.TransitionIn.DurationFrames < 0 {
		return fmt.Errorf("overlay_style.transition_in.duration_frames is negative")
	}
	style := &scriptpkg.OverlayStyleSpec{
		FontFamily: o.FontFamily, GlowSize: o.GlowSize, StrokeSize: o.StrokeSize,
	}
	if o.Size != nil {
		style.Size = &scriptpkg.OverlaySizeSpec{FontSize: o.Size.FontSize}
	}
	if err := style.Validate(); err != nil {
		return err
	}
	return nil
}

func (p Profile) validateSoundEffects() error {
	for i, s := range p.SoundEffects {
		if strings.TrimSpace(s.AssetID) == "" {
			return fmt.Errorf("sound_effects[%d] has no asset_id", i)
		}
		if s.GainDB != nil && *s.GainDB > 0 {
			// Canonical rule (EditingAssetsPolicy): a profile may attenuate,
			// never boost — the master mix owns the final level.
			return fmt.Errorf("sound_effects[%d] (%s) gain_db=%g must be <= 0 dB", i, s.AssetID, *s.GainDB)
		}
		if s.AtMS < 0 {
			return fmt.Errorf("sound_effects[%d] (%s) at_ms is negative", i, s.AssetID)
		}
		if s.SceneID != "" && s.AtMS != 0 {
			// Mirror of SoundEffectIntent's contract: absolute and
			// scene-relative placement are mutually exclusive.
			return fmt.Errorf("sound_effects[%d] (%s) sets both at_ms and scene_id", i, s.AssetID)
		}
		if s.SceneID != "" {
			if _, err := scriptpkg.SFXAnchor(s.Anchor).Normalize(); err != nil {
				return fmt.Errorf("sound_effects[%d] (%s): %w", i, s.AssetID, err)
			}
		} else if anchor := strings.TrimSpace(s.Anchor); anchor != "" {
			if _, err := scriptpkg.SFXAnchor(anchor).Normalize(); err != nil {
				return fmt.Errorf("sound_effects[%d] (%s): %w", i, s.AssetID, err)
			}
		}
		if s.SourceInMS < 0 {
			return fmt.Errorf("sound_effects[%d] (%s) source_in_ms is negative", i, s.AssetID)
		}
		if s.DurationMS < 0 {
			return fmt.Errorf("sound_effects[%d] (%s) duration_ms is negative", i, s.AssetID)
		}
	}
	return nil
}

func (p Profile) validateMixPolicy() error {
	if strings.TrimSpace(p.MixPolicy) == "" {
		return nil
	}
	// Normalize maps the wire aliases and returns "" for anything unknown, so
	// an unnormalizable policy fails here instead of silently becoming "no
	// policy" at compile time.
	if capabilityaudio.AudioMixPolicy(p.MixPolicy).Normalize() == "" {
		return fmt.Errorf("mix_policy %q is not a canonical audio mix policy (VOICEOVER_ONLY, VOICEOVER_DUCKED_CLIP)", p.MixPolicy)
	}
	return nil
}

func (p Profile) validatePhraseMotions() error {
	if len(p.PhraseMotions) == 0 {
		return nil
	}
	certified := make(map[string]bool)
	for _, id := range overlays.CertifiedPhraseMotions() {
		certified[id] = true
	}
	seen := make(map[string]bool, len(p.PhraseMotions))
	for _, id := range p.PhraseMotions {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return fmt.Errorf("phrase_motions carries an empty id")
		}
		if !certified[trimmed] {
			return fmt.Errorf("phrase_motions id %q is not a certified phrase motion", trimmed)
		}
		if seen[trimmed] {
			return fmt.Errorf("phrase_motions repeats %q (a rotation pool must be distinct)", trimmed)
		}
		seen[trimmed] = true
	}
	return nil
}

func (p Profile) validateImageMotions() error {
	if len(p.ImageMotions) == 0 {
		return nil
	}
	return fmt.Errorf("image_motions is deprecated and unsupported; generated images use certified 2D image presets")
}
