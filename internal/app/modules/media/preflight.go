package media

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

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

// NewPreflight also validates explicitly enabled VidRush providers against
// the frozen composition registry.
func NewPreflight(deps PreflightDeps) scriptgen.MediaPreflight {
	return &preflightAdapter{
		providerAvailability: deps.ProviderAvailability,
		clipProber:           &assetServiceClipProber{assets: deps.Assets},
		audioAssetSource:     deps.AudioAssetSource,
		clipAudioAssetSource: deps.ClipAudioAssetSource,
		imageProviderHealth:  deps.ImageProviderHealth,
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
	if err := probeLocalLocation(details); err != nil {
		return fmt.Errorf("clip %q: %w", clipID, err)
	}
	return nil
}

// probeLocalLocation verifies that a Creator-local location recorded in the
// registry still has bytes on this host. Registry presence is not availability:
// the measured 2026-11-17..20 window burned 1.08 h in 13 publishing jobs that
// discovered a missing local source only inside the remote final job, after the
// whole render had already been paid for.
//
// Only filesystem locations are checked. An asset whose bytes live in an
// object store is left to its own resolver: stat-ing a URI there would turn a
// valid remote-mode asset into a false preflight failure, which this ticket
// explicitly forbids.
func probeLocalLocation(details *asset.Details) error {
	location := details.LocalLocation()
	if location == nil {
		return nil
	}
	raw := strings.TrimSpace(location.URI)
	if raw == "" {
		return nil
	}
	path := raw
	if strings.HasPrefix(raw, "file://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("creator-local location %q is not a valid file URI: %w", raw, err)
		}
		path = parsed.Path
	} else if strings.Contains(raw, "://") {
		// s3://, gs://, https://: not a filesystem path.
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("creator-local source %q unavailable: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("creator-local source %q is a directory, not a file", path)
	}
	return nil
}
