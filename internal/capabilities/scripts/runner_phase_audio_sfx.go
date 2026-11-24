package scriptgeneration

// runner_phase_audio_sfx.go wires the overlay-SFX projection into the master
// audio compile. The overlay plan is final at this point, so its image windows
// become absolute cues on the plan the Rust renderer receives. This is the ONE
// place that resolves those cue assets: the same AudioAssetSource that serves
// audio.sound_effects, so an overlay cue can never come from a private
// manifest.

import (
	"context"
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"go.uber.org/zap"
)

// extendAudioPlanWithOverlaySFX appends the overlay plan's image cues to the
// compiled master plan and returns the re-sealed plan plus the merged asset
// table. A cue past the master timeline is inaudible and skipped with a
// warning; every other failure is fail-closed.
func (r *Runner) extendAudioPlanWithOverlaySFX(
	ctx context.Context,
	runID string,
	req contract.GenerateRequest,
	result *contract.GenerateResult,
	timeline capabilityaudio.CanonicalTimeline,
	plan capabilityaudio.CompiledAudioPlan,
	assets capabilityaudio.ResolvedAudioAssets,
) (capabilityaudio.CompiledAudioPlan, capabilityaudio.ResolvedAudioAssets, error) {
	if result == nil {
		return plan, assets, nil
	}
	intents, err := overlaySFXIntentsForPlan(req, result.OverlayPlan, r.soundEffectPool(ctx, runID))
	if err != nil {
		return plan, assets, err
	}
	if len(intents) == 0 {
		return plan, assets, nil
	}
	source := r.audioAssetSource
	if result.AudioPrefetch != nil && result.AudioPrefetch.AudioSource != nil {
		source = result.AudioPrefetch.AudioSource
	}
	if source == nil {
		return plan, assets, fmt.Errorf("overlay SFX requires the audio asset resolver, which is not wired")
	}
	merged := append(capabilityaudio.ResolvedAudioAssets(nil), assets...)
	known := make(map[string]struct{}, len(merged))
	for _, asset := range merged {
		known[asset.AssetID] = struct{}{}
	}
	resolvedByID := make(map[string]struct{}, len(intents))
	layers := make([]capabilityaudio.AudioLayer, 0, len(intents))
	for _, intent := range intents {
		startUS := intent.AtMS * 1000
		if startUS >= timeline.DurationUS {
			r.log.Warn("overlay SFX cue starts after the master audio timeline; cue skipped",
				zap.String("run_id", runID),
				zap.String("asset_id", intent.AssetID),
				zap.Int64("at_ms", intent.AtMS),
				zap.Int64("master_duration_us", timeline.DurationUS))
			continue
		}
		durationUS := intent.DurationMS * 1000
		if remaining := timeline.DurationUS - startUS; durationUS > remaining {
			durationUS = remaining
		}
		if durationUS <= 0 {
			continue
		}
		publicID := strings.TrimSpace(intent.AssetID)
		if _, ok := resolvedByID[publicID]; !ok {
			resolved, err := source.ResolveAudioAsset(ctx, capabilityaudio.CanonicalAssetID(publicID))
			if err != nil {
				return plan, assets, fmt.Errorf("overlay SFX: resolve audio asset %q: %w", publicID, err)
			}
			if strings.TrimSpace(resolved.Path) == "" {
				return plan, assets, fmt.Errorf("overlay SFX: audio asset %q resolved to an empty path", publicID)
			}
			// The plan references the caller-visible alias, exactly like the
			// audio intent resolver: the registry identity stays physical.
			resolved.AssetID = publicID
			if _, exists := known[publicID]; !exists {
				merged = append(merged, resolved)
				known[publicID] = struct{}{}
			}
			resolvedByID[publicID] = struct{}{}
		}
		layers = append(layers, capabilityaudio.AudioLayer{
			AssetID:         publicID,
			TimelineStartUS: startUS,
			DurationUS:      durationUS,
			GainDB:          intent.GainDB,
		})
	}
	if err := plan.AppendSFXLayers(timeline, layers); err != nil {
		return plan, assets, err
	}
	return plan, merged, nil
}

// soundEffectPool adapts the runner's audio source to the audio package's
// selection filter. It stays here because it is the only part that needs the
// Runner's narrow AudioAssetSource field; the filtering itself belongs to the
// audio target package.
func (r *Runner) soundEffectPool(ctx context.Context, runID string) []string {
	if r == nil || r.audioAssetSource == nil {
		return nil
	}
	source, ok := r.audioAssetSource.(scriptaudio.SoundEffectPoolSource)
	if !ok {
		return nil
	}
	candidates, err := source.ListSoundEffectCandidates(ctx)
	if err != nil {
		if r.log != nil {
			r.log.Warn("sound-effect pool unavailable; falling back to built-in whoosh aliases",
				zap.String("run_id", runID), zap.Error(err))
		}
		return nil
	}
	return scriptaudio.FilterSoundEffectCandidates(candidates)
}
