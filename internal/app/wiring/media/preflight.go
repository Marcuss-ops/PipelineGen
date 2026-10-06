package media

import (
	"context"
	"fmt"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// AssetDetailsLookup is the narrow asset-details read surface shared by the
// media preflight and the audio/overlay resolvers. *detail.Service (SQLite
// degrade mode) and *pgmedia.AssetDetailsReader (media SSOT) both satisfy it,
// so the readers cannot drift onto a different engine than the one they own.
type AssetDetailsLookup interface {
	Get(ctx context.Context, id string) (*asset.Details, error)
}

// NewPreflight binds the canonical asset registry and audio sources to the
// script-generation MediaPreflight port. The policy remains owned by
// capabilities/scripts; this package owns composition only.
func NewPreflight(assets AssetDetailsLookup, audioAssetSource scriptgen.AudioAssetSource, clipAudioAssetSource scriptgen.ClipAudioAssetSource) scriptgen.MediaPreflight {
	return NewPreflightWithImageProviderHealth(assets, audioAssetSource, clipAudioAssetSource, nil)
}

// NewPreflightWithImageProviderHealth is NewPreflight with the image-provider
// health probe wired. The probe runs only for requests that actually depend on
// internet image retrieval (internet_images enabled or the entity-image
// surface requested), so a clip-only run is never failed by a degraded image
// provider it would never have used.
func NewPreflightWithImageProviderHealth(assets AssetDetailsLookup, audioAssetSource scriptgen.AudioAssetSource, clipAudioAssetSource scriptgen.ClipAudioAssetSource, imageProviderHealth scriptgen.ImageProviderHealthProbe) scriptgen.MediaPreflight {
	return NewPreflightWithProviderAvailability(assets, audioAssetSource, clipAudioAssetSource, imageProviderHealth, nil)
}

// NewPreflightWithProviderAvailability also validates explicitly enabled
// VidRush providers against the frozen composition registry.
func NewPreflightWithProviderAvailability(assets AssetDetailsLookup, audioAssetSource scriptgen.AudioAssetSource, clipAudioAssetSource scriptgen.ClipAudioAssetSource, imageProviderHealth scriptgen.ImageProviderHealthProbe, providerAvailability scriptgen.VidRushProviderAvailabilityProbe) scriptgen.MediaPreflight {
	return &preflightAdapter{
		providerAvailability: providerAvailability,
		clipProber:           &assetServiceClipProber{assets: assets},
		audioAssetSource:     audioAssetSource,
		clipAudioAssetSource: clipAudioAssetSource,
		imageProviderHealth:  imageProviderHealth,
	}
}

type preflightAdapter struct {
	clipProber           scriptgen.ClipPreflighter
	audioAssetSource     scriptgen.AudioAssetSource
	clipAudioAssetSource scriptgen.ClipAudioAssetSource
	imageProviderHealth  scriptgen.ImageProviderHealthProbe
	providerAvailability scriptgen.VidRushProviderAvailabilityProbe
}

// ImageProviderHealthSurface is the narrow image-capability surface the
// preflight needs: it reports whether at least one retrieval provider is
// healthy. *images.Service satisfies it.
type ImageProviderHealthSurface interface {
	ProbeImageProviderHealth(ctx context.Context) error
}

// NewImageProviderHealthProbe adapts an image-provider health surface to the
// script-generation media-preflight port. A nil surface returns nil so the
// preflight stays a no-op for compositions without the images capability.
func NewImageProviderHealthProbe(surface ImageProviderHealthSurface) scriptgen.ImageProviderHealthProbe {
	if surface == nil {
		return nil
	}
	return imageProviderHealthProbe{surface: surface}
}

type imageProviderHealthProbe struct {
	surface ImageProviderHealthSurface
}

type vidRushProviderAvailabilityProbe struct {
	providerLookup interface {
		Provider(name string) (scriptports.VidRushAssetProvider, error)
	}
}

var _ scriptgen.VidRushProviderAvailabilityProbe = vidRushProviderAvailabilityProbe{}

func NewVidRushProviderAvailabilityProbe(providerLookup interface {
	Provider(name string) (scriptports.VidRushAssetProvider, error)
}) scriptgen.VidRushProviderAvailabilityProbe {
	if providerLookup == nil {
		return nil
	}
	return vidRushProviderAvailabilityProbe{providerLookup: providerLookup}
}

func (p vidRushProviderAvailabilityProbe) ProbeVidRushProvider(_ context.Context, provider string) error {
	if p.providerLookup == nil {
		return fmt.Errorf("VidRush provider registry not wired")
	}
	_, err := p.providerLookup.Provider(provider)
	return err
}

var _ scriptgen.ImageProviderHealthProbe = imageProviderHealthProbe{}

func (p imageProviderHealthProbe) ProbeImageProviderHealth(ctx context.Context) error {
	if p.surface == nil {
		return fmt.Errorf("image provider health probe not wired")
	}
	return p.surface.ProbeImageProviderHealth(ctx)
}

var _ scriptgen.MediaPreflight = (*preflightAdapter)(nil)

func (a *preflightAdapter) RunVidRushProviderAvailability(ctx context.Context, req scriptgen.GenerateRequest) scriptgen.PreflightResult {
	return scriptgen.RunVidRushProviderAvailabilityPreflight(ctx, req.MediaPlan, a.providerAvailability)
}

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

	in := scriptgen.MediaPreflightInput{
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
	}
	in.MediaPlan = req.MediaPlan
	in.VidRushProviderAvailability = a.providerAvailability
	if a.imageProviderHealth != nil && requestNeedsImageProviders(req) {
		in.ImageProviderHealth = a.imageProviderHealth
	}
	return scriptgen.RunMediaPreflight(ctx, in)
}

// requestNeedsImageProviders reports whether the request's media plan depends
// on external image retrieval, so the health probe is only meaningful then.
func requestNeedsImageProviders(req scriptgen.GenerateRequest) bool {
	if req.MediaPlan.ProviderPolicy.InternetImages.AsBool() {
		return true
	}
	return req.MediaPlan.Extraction.EntityImageSurfaceEnabled()
}

type assetServiceClipProber struct {
	assets AssetDetailsLookup
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
