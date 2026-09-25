// Package scriptgeneration — channel_profile_apply.go applies a channel
// profile to a request the SAME way ApplyEditingAssetPolicy applies the
// editorial asset policy: a pure default-fill of the choices the caller left
// BLANK, at the single ingress point both job handlers share.
//
// The rules, in order of importance:
//
//  1. Caller intent always wins. An existing subtitles block, watermark,
//     overlay style, SFX list, mix policy or motion pool is never touched.
//  2. A profile completes a feature the caller already opted into: the
//     visual blocks are gated on render.enabled and the audio blocks on
//     COMBINED_TIMELINE, the same gates ApplyEditingAssetPolicy uses — a
//     profile never turns an audio-only run into a render, and never plants
//     inert payload on a mode that would drop it.
//  3. Defaults still come from VideoRenderSpec.Normalize: this file decides
//     WHAT a channel asks for, Normalize decides what an incomplete request
//     looks like. Re-running it after the fill is idempotent — it only fills
//     fields that are still empty.
package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/channelprofile"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ApplyChannelProfile fills the blanks of req from p. An empty profile is a
// no-op; a nil request fails closed.
func ApplyChannelProfile(req *GenerateRequest, p channelprofile.Profile) error {
	if req == nil {
		return fmt.Errorf("apply channel profile: request is required")
	}
	if err := p.Validate(); err != nil {
		// The registry validates at load, so reaching this means the profile
		// was built in code or the registry was bypassed: fail closed rather
		// than apply a half-checked default set.
		return fmt.Errorf("apply channel profile: %w", err)
	}

	visualFilled := false
	if req.Render.Enabled && req.Render.Subtitles == nil && p.Subtitles != nil && channelWantsSubtitles(p.Subtitles) {
		style, err := scriptpkg.ResolveSubtitleStyle(p.Subtitles.Preset, p.Subtitles.Style.VisualStyle())
		if err != nil {
			return fmt.Errorf("apply channel profile %q: %w", p.ChannelID, err)
		}
		req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{
			Enabled: true,
			Mode:    p.Subtitles.Mode,
			// Preset feeds the clip-render projection; StyleID is the same
			// choice spelled for the ASS burn path, which resolves its
			// typography by style-id substring (ResolveFontPreset). One
			// channel decision, both render paths, no second vocabulary.
			Preset:  p.Subtitles.Preset,
			StyleID: p.Subtitles.Preset,
			Style:   style,
		}
		visualFilled = true
	}
	if req.Render.Enabled && req.Render.Watermark == nil && p.Watermark != nil {
		req.Render.Watermark = p.Watermark.Watermark()
		visualFilled = true
	}
	if visualFilled {
		// Normalize owns the canonical default fill (subtitle mode/color/
		// font/position, watermark opacity/margin/position/color/size). It
		// preserves every explicit value above, so this stays idempotent for
		// the caller-owned parts of the spec.
		req.Render.Normalize()
	}

	if req.OverlayStyle == nil && p.OverlayStyle != nil {
		req.OverlayStyle = p.OverlayStyle.OverlayStyle()
	}
	if req.PhraseMotionFamily == "" && len(req.PhraseMotions) == 0 && len(p.PhraseMotions) > 0 {
		req.PhraseMotions = append([]string(nil), p.PhraseMotions...)
	}
	if len(req.ImageMotions) == 0 && len(p.ImageMotions) > 0 {
		req.ImageMotions = append([]string(nil), p.ImageMotions...)
	}

	// Audio blocks share the ApplyEditingAssetPolicy gate: layer intents and
	// the mix policy only reach the canonical pipeline on COMBINED_TIMELINE.
	if audioRunsLayers(req) {
		if req.MixPolicy == "" && strings.TrimSpace(p.MixPolicy) != "" {
			req.MixPolicy = capabilityaudio.AudioMixPolicy(p.MixPolicy).Normalize()
		}
		if len(req.SoundEffects) == 0 && len(p.SoundEffects) > 0 {
			effects, err := p.SoundEffectsIntents()
			if err != nil {
				return fmt.Errorf("apply channel profile %q: %w", p.ChannelID, err)
			}
			req.SoundEffects = effects
		}
	}
	return nil
}

// channelWantsSubtitles interprets the profile's tri-state: a present block
// wants subtitles unless it says enabled:false explicitly.
func channelWantsSubtitles(s *channelprofile.SubtitlesProfile) bool {
	if s == nil {
		return false
	}
	if s.Enabled != nil {
		return *s.Enabled
	}
	return true
}

// audioRunsLayers mirrors the ApplyEditingAssetPolicy gate: BGM/SFX layer
// intents only reach the canonical pipeline on the combined-timeline mode,
// so injecting them anywhere else would be inert payload.
func audioRunsLayers(req *GenerateRequest) bool {
	return req.Audio == capabilityaudio.AudioModeCombinedTimeline
}
