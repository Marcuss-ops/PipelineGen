package media

import (
	"context"
	"fmt"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// NewPreflight binds the canonical asset registry and audio sources to the
// script-generation MediaPreflight port. The policy remains owned by
// capabilities/scripts; this package owns composition only.
func NewPreflight(assets *detail.Service, audioAssetSource scriptgen.AudioAssetSource, clipAudioAssetSource scriptgen.ClipAudioAssetSource) scriptgen.MediaPreflight {
	return &preflightAdapter{
		clipProber:           &assetServiceClipProber{assets: assets},
		audioAssetSource:     audioAssetSource,
		clipAudioAssetSource: clipAudioAssetSource,
	}
}

type preflightAdapter struct {
	clipProber           scriptgen.ClipPreflighter
	audioAssetSource     scriptgen.AudioAssetSource
	clipAudioAssetSource scriptgen.ClipAudioAssetSource
}

var _ scriptgen.MediaPreflight = (*preflightAdapter)(nil)

func (a *preflightAdapter) Run(ctx context.Context, req scriptgen.GenerateRequest) scriptgen.PreflightResult {
	clipIDs := make([]string, 0, len(req.Source.ClipIDs)+4)
	clipIDs = append(clipIDs, req.Source.ClipIDs...)
	for _, seg := range req.ScriptParams.Segments {
		clipIDs = append(clipIDs, seg.ClipIDs...)
	}
	if req.Intro != nil {
		clipIDs = append(clipIDs, req.Intro.NormalizedClipIDs()...)
	}
	if req.Outro != nil {
		clipIDs = append(clipIDs, req.Outro.NormalizedClipIDs()...)
	}

	fixedClips := make([]scriptgen.FixedClipPreflight, 0, 4)
	fixedSections := make([]scriptgen.FixedSectionPreflight, 0, 2)
	for _, fixedSection := range []struct {
		name    string
		section *scriptpkg.FixedSection
	}{
		{name: "intro", section: req.Intro},
		{name: "outro", section: req.Outro},
	} {
		name, section := fixedSection.name, fixedSection.section
		if section == nil {
			continue
		}
		playback := section.NormalizedPlayback()
		sectionClipIDs := section.NormalizedClipIDs()
		fixedSections = append(fixedSections, scriptgen.FixedSectionPreflight{
			Name: name, ClipIDs: sectionClipIDs, Playback: playback,
		})
		for _, clipID := range sectionClipIDs {
			fixedClips = append(fixedClips, scriptgen.FixedClipPreflight{
				ClipID: clipID, SourceInMS: playback.SourceInMS, SourceOutMS: playback.SourceOutMS,
			})
		}
	}

	var bgmIDs, sfxIDs []string
	for _, b := range req.BackgroundMusic {
		if b.AssetID != "" {
			bgmIDs = append(bgmIDs, b.AssetID)
		}
	}
	for _, s := range req.SoundEffects {
		if s.AssetID != "" {
			sfxIDs = append(sfxIDs, s.AssetID)
		}
	}

	var watermarkID string
	if req.Render.Watermark != nil && req.Render.Watermark.Enabled {
		watermarkID = req.Render.Watermark.AssetID
	}

	var backgroundID string
	if req.Render.Background != nil && req.Render.Background.Mode == "asset" {
		backgroundID = req.Render.Background.AssetID
	}

	return scriptgen.RunMediaPreflight(ctx, scriptgen.MediaPreflightInput{
		ClipIDs:            clipIDs,
		FixedClips:         fixedClips,
		FixedSections:      fixedSections,
		ClipProber:         a.clipProber,
		ClipAudioSource:    a.clipAudioAssetSource,
		MixPolicy:          req.MixPolicy,
		BGMIDs:             bgmIDs,
		SFXIDs:             sfxIDs,
		AudioAssetSource:   a.audioAssetSource,
		RenderEnabled:      req.Render.Enabled,
		WatermarkAssetID:   watermarkID,
		WatermarkResolver:  a.clipProber,
		BackgroundAssetID:  backgroundID,
		BackgroundResolver: a.clipProber,
	})
}

type assetServiceClipProber struct {
	assets *detail.Service
}

var _ scriptgen.ClipPreflighter = (*assetServiceClipProber)(nil)

func (p *assetServiceClipProber) ProbeClip(ctx context.Context, clipID string) error {
	if p == nil || p.assets == nil {
		return fmt.Errorf("clip prober not wired — cannot verify clip %q existence", clipID)
	}
	details, err := p.assets.Get(ctx, clipID)
	if err != nil {
		return fmt.Errorf("clip %q: %w", clipID, err)
	}
	if details == nil || details.Asset == nil {
		return fmt.Errorf("clip %q not found in registry", clipID)
	}
	return nil
}
