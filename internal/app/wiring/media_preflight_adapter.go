// Package capabilities — media_preflight_adapter.go wires the concrete
// MediaPreflight adapter for the P0.5 fail-fast media verification.
//
// The capability (internal/capabilities/scripts) owns the port
// (MediaPreflight interface) and the pure function (RunMediaPreflight).
// THIS file (composition root) owns the mechanics:
//
//   - Translating GenerateRequest → MediaPreflightInput
//   - Clip probing via detail.Service.Get (fast existence check)
//   - BGM/SFX/watermark resolution via the already-wired audioAssetSourceAdapter
//   - Clip audio verification via the same adapter
//
// The adapter runs in parallel with Gemma scene text generation; the runner
// joins after scene text completes and fails the run BEFORE any TTS work
// if any media requirement is not met.
package wiring

import (
	"context"
	"fmt"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// mediaPreflightAdapter implements MediaPreflight by translating
// a GenerateRequest into a MediaPreflightInput and delegating to the pure
// RunMediaPreflight function. It is wired once at composition time and
// reused across runs.
type mediaPreflightAdapter struct {
	clipProber           scriptgen.ClipPreflighter
	audioAssetSource     scriptgen.AudioAssetSource
	clipAudioAssetSource scriptgen.ClipAudioAssetSource
}

// Compile-time assertion.
var _ scriptgen.MediaPreflight = (*mediaPreflightAdapter)(nil)

// Run translates a batch GenerateRequest into the canonical preflight
// input and runs every check concurrently. Failures are collected so the
// operator sees the COMPLETE picture in one run instead of failing →
// fixing → failing across N retries.
func (a *mediaPreflightAdapter) Run(ctx context.Context, req scriptgen.GenerateRequest) scriptgen.PreflightResult {
	// Collect clip IDs (including literal intro/outro sections which bypass LLM).
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
	for _, section := range []*scriptpkg.FixedSection{req.Intro, req.Outro} {
		if section == nil {
			continue
		}
		playback := section.NormalizedPlayback()
		for _, clipID := range section.NormalizedClipIDs() {
			fixedClips = append(fixedClips, scriptgen.FixedClipPreflight{
				ClipID: clipID, SourceInMS: playback.SourceInMS, SourceOutMS: playback.SourceOutMS,
			})
		}
	}

	// Collect BGM + SFX IDs.
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

	// Watermark asset ID.
	var watermarkID string
	if req.Render.Watermark != nil && req.Render.Watermark.Enabled {
		watermarkID = req.Render.Watermark.AssetID
	}

	// Background asset ID — probed only for background.mode=asset (the
	// blur_source/none modes carry no asset block).
	var backgroundID string
	if req.Render.Background != nil && req.Render.Background.Mode == "asset" {
		backgroundID = req.Render.Background.AssetID
	}

	return scriptgen.RunMediaPreflight(ctx, scriptgen.MediaPreflightInput{
		ClipIDs:            clipIDs,
		IntroClipIDs:       req.Source.IntroClipIDs,
		FixedClips:         fixedClips,
		ClipProber:         a.clipProber,
		ClipAudioSource:    a.clipAudioAssetSource,
		MixPolicy:          req.MixPolicy,
		BGMIDs:             bgmIDs,
		SFXIDs:             sfxIDs,
		AudioAssetSource:   a.audioAssetSource,
		RenderEnabled:      req.Render.Enabled,
		WatermarkAssetID:   watermarkID,
		WatermarkResolver:  a.clipProber, // same prober works for watermark assets
		BackgroundAssetID:  backgroundID,
		BackgroundResolver: a.clipProber,
	})
}

// ────────────────────────────────────────────────────────────────────────
// assetServiceClipProber implements scriptgen.ClipPreflighter by probing
// the canonical detail.Service for a non-deleted row. A fast existence
// check — no materialization, no Drive download.
// ────────────────────────────────────────────────────────────────────────

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
