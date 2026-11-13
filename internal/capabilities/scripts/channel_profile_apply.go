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
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
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
	// Entity-card composition family: caller intent wins (the request's own
	// pin is authoritative even when it names a different tag), a profile
	// completes only a blank. An empty value means "keep the certified
	// random selector" — the planner never sees a channel-forced "random".
	if strings.TrimSpace(req.EntityStyleID) == "" && strings.TrimSpace(p.EntityStyleID) != "" {
		req.EntityStyleID = strings.TrimSpace(p.EntityStyleID)
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

// applyOverlayBackgroundSelection fills the OVERLAY render's background plate
// when the caller left it blank. It inherits a caller's registered video plate
// first; otherwise it uses the same curated pool and seed as
// applyBackgroundSelection, so both render surfaces stay on one plate.
//
// It is deliberately gated on req.Render.Enabled: the overlay pipeline treats a
// non-nil OverlayBackground as a request to compile an overlay plan, so filling
// it for a job that never asked for a render would silently ENABLE overlay
// rendering. Filling only ever completes a render the caller already requested.
// An explicit caller selection is preserved untouched — including kind "color",
// which is how a caller deliberately asks for the black canvas this default
// would otherwise replace.
func applyOverlayBackgroundSelection(req *GenerateRequest, policy mediaregistry.EditingAssetsPolicy, seed string) error {
	if req == nil {
		return nil
	}
	// Overlay-only jobs use the per-item renderer, so an explicitly requested
	// plate belongs inside each short overlay clip. Do not auto-select a plate
	// when none was requested: that keeps the export limited to actual overlay
	// windows and avoids a scene-wide background timeline.
	if req.Render.OverlaysOnly {
		return nil
	}
	if !req.Render.Enabled {
		return nil
	}
	// Final-job overlays are encoded as opaque video clips and placed in
	// replace-mode windows. A caller's solid color canvas would therefore hide
	// the assembled picture for the entire overlay window. Replace only that
	// unsafe final-job case with the render background when the caller named
	// one; otherwise fall back to a curated moving plate. When the render
	// background is an asset, let the shared-source block below inherit it in
	// one place instead of selecting an unrelated plate here (r110).
	if req.FinalJob && req.OverlayBackground != nil && strings.EqualFold(strings.TrimSpace(req.OverlayBackground.Kind), "color") {
		renderBackground := req.Render.Background
		hasRenderPlate := renderBackground != nil &&
			strings.EqualFold(strings.TrimSpace(renderBackground.Mode), videoBackgroundModeAsset)
		if !hasRenderPlate {
			plate, err := policy.SelectBackgroundForChannel(seed, req.ChannelID)
			if err != nil {
				return fmt.Errorf("apply editing asset policy: final overlay background: %w", err)
			}
			req.OverlayBackground = &scriptpkg.OverlayBackgroundSpec{
				Kind: overlayBackgroundPlateKind, AssetID: plate.ID,
				Fit: overlayBackgroundPlateFit, Loop: true,
			}
			return nil
		}
	}
	// A final-job overlay is handed off as a full-frame replacement window.
	// A solid color therefore hides the picture underneath for the complete
	// window (r79 produced a white interval when a phrase was rendered on an
	// opaque white canvas). Keep explicit color plates for standalone renders,
	// but final jobs must use a moving editorial plate so a text-only overlay
	// cannot turn into a blank white/black screen in the delivered video.
	// The assembled video background is the single source of truth for every
	// overlay scene. Override a conflicting overlay_background declaration so
	// image and phrase renders cannot publish on a second plate (for example a
	// black gradient) while the final video uses a channel background.
	if background := req.Render.Background; background != nil &&
		strings.EqualFold(strings.TrimSpace(background.Mode), videoBackgroundModeAsset) {
		assetID := strings.TrimSpace(background.AssetID)
		kind := ""
		if plate, ok := mediaregistry.ResolveEditorialBackgroundReference(assetID); ok {
			assetID = plate.ID
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(plate.MediaType)), "video/") {
				kind = overlayBackgroundPlateKind
			}
		}
		if assetID == "" {
			return fmt.Errorf("apply editing asset policy: overlay background: render background mode=asset requires asset_id")
		}
		if kind != "image" && kind != "video" {
			return fmt.Errorf("apply editing asset policy: overlay background: render background asset %q requires kind=image or kind=video", assetID)
		}
		req.OverlayBackground = &scriptpkg.OverlayBackgroundSpec{
			Kind: kind, AssetID: assetID, Fit: overlayBackgroundPlateFit, Loop: kind == "video",
		}
		return nil
	}
	if !overlayBackgroundSelectionIsBlank(req.OverlayBackground) {
		return nil
	}
	plate, err := policy.SelectBackgroundForChannel(seed, req.ChannelID)
	if err != nil {
		return fmt.Errorf("apply editing asset policy: overlay background: %w", err)
	}
	req.OverlayBackground = &scriptpkg.OverlayBackgroundSpec{
		Kind:    overlayBackgroundPlateKind,
		AssetID: plate.ID,
		Fit:     overlayBackgroundPlateFit,
		Loop:    true,
	}
	return nil
}

// overlayBackgroundSelectionIsBlank reports whether the caller left the overlay
// background unspecified. Any declared kind is caller intent and is preserved:
// notably kind "color" is the deliberate black canvas, and a kind that carries
// no identity yet (kind "video" with the asset resolved later) is still the
// caller's choice.
func overlayBackgroundSelectionIsBlank(spec *scriptpkg.OverlayBackgroundSpec) bool {
	if spec == nil {
		return true
	}
	return strings.TrimSpace(spec.Kind) == "" &&
		strings.TrimSpace(spec.AssetID) == "" &&
		strings.TrimSpace(spec.URL) == "" &&
		strings.TrimSpace(spec.SHA256) == ""
}
