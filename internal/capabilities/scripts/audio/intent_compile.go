package audio

import (
	"context"
	"fmt"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// IntentCompileResult contains the sealed plan and resolved asset table for
// BGM/SFX intents. Audio asset resolution time remains separately observable.
type IntentCompileResult struct {
	Plan                capabilityaudio.CompiledAudioPlan
	Assets              capabilityaudio.ResolvedAudioAssets
	AudioAssetResolveMS int64
}

// CompileWithIntents resolves BGM and SFX intents and compiles them into the
// canonical audio plan. Every time and source trim is validated before a plan
// is returned.
func CompileWithIntents(
	ctx context.Context,
	timeline capabilityaudio.CanonicalTimeline,
	profile capabilityaudio.CanonicalAudioProfile,
	policy capabilityaudio.AudioMixPolicy,
	bgmIntents []scriptpkg.BackgroundMusicIntent,
	sfxIntents []scriptpkg.SoundEffectIntent,
	source AssetSource,
) (IntentCompileResult, error) {
	fail := func(err error) (IntentCompileResult, error) { return IntentCompileResult{}, err }

	assetResolveStarted := time.Now()
	assetResolver, err := NewAssetResolver(source)
	if err != nil {
		return fail(err)
	}
	assets, err := assetResolver.Resolve(ctx, bgmIntents, sfxIntents)
	if err != nil {
		return fail(err)
	}
	assetResolveMS := time.Since(assetResolveStarted).Milliseconds()
	durationByID := make(map[string]int64, len(assets))
	for _, asset := range assets {
		durationByID[asset.AssetID] = asset.DurationUS
	}

	resolvedBGM, err := NewBackgroundMusicResolver().Resolve(timeline, bgmIntents)
	if err != nil {
		return fail(err)
	}
	var bgmLayers []capabilityaudio.AudioLayer
	for _, layer := range resolvedBGM {
		expanded, err := NewAudioLoopExpander().Expand(layer, durationByID[layer.AssetID])
		if err != nil {
			return fail(fmt.Errorf("expand bgm %s: %w", layer.AssetID, err))
		}
		bgmLayers = append(bgmLayers, expanded...)
	}

	resolvedSFX, err := NewAudioIntentResolver().ResolveSoundEffects(timeline, sfxIntents)
	if err != nil {
		return fail(err)
	}
	var sfxLayers []capabilityaudio.AudioLayer
	for _, sfx := range resolvedSFX {
		duration := sfx.DurationUS
		if duration <= 0 {
			duration = durationByID[sfx.AssetID]
			if duration <= 0 {
				return fail(fmt.Errorf("sfx %s has no explicit duration and its source duration is unknown", sfx.AssetID))
			}
		}
		if sfx.TimelineStartUS > timeline.DurationUS-duration {
			return fail(fmt.Errorf("sfx %s placement [%d,%d) exceeds the %dus timeline", sfx.AssetID, sfx.TimelineStartUS, sfx.TimelineStartUS+duration, timeline.DurationUS))
		}
		if sourceDuration := durationByID[sfx.AssetID]; sourceDuration > 0 && sfx.SourceInUS > sourceDuration-duration {
			return fail(fmt.Errorf("sfx %s source trim [%d,%d) overruns the %dus source", sfx.AssetID, sfx.SourceInUS, sfx.SourceInUS+duration, sourceDuration))
		}
		sfxLayers = append(sfxLayers, capabilityaudio.AudioLayer{
			AssetID: sfx.AssetID, TimelineStartUS: sfx.TimelineStartUS,
			DurationUS: duration, SourceInUS: sfx.SourceInUS, GainDB: sfx.GainDB,
		})
	}

	automationCompiler := NewAudioAutomationCompiler()
	fades, err := automationCompiler.CompileBGMFades(resolvedBGM)
	if err != nil {
		return fail(err)
	}
	ducking, err := automationCompiler.CompileBGMDucking(timeline, resolvedBGM)
	if err != nil {
		return fail(err)
	}
	automation := append(fades, ducking...)
	plan, err := capabilityaudio.CompileWithLayersAndPolicy(timeline, profile, bgmLayers, sfxLayers, automation, policy)
	if err != nil {
		return fail(err)
	}
	return IntentCompileResult{Plan: plan, Assets: assets, AudioAssetResolveMS: assetResolveMS}, nil
}

// mergeResolvedAudioAssets merges the primary (voiceover/original-clip) asset
// table with the resolved BGM/SFX table, preserving the primary-first order
// and de-duplicating on asset_id. The two tables are disjoint by construction
// (scene-bound VO/clip ids vs intent asset ids); the dedup is a safety net.
func MergeResolvedAssets(primary, layers capabilityaudio.ResolvedAudioAssets) capabilityaudio.ResolvedAudioAssets {
	if len(layers) == 0 {
		return primary
	}
	out := make(capabilityaudio.ResolvedAudioAssets, 0, len(primary)+len(layers))
	seen := make(map[string]struct{}, len(primary)+len(layers))
	for _, asset := range append(append(capabilityaudio.ResolvedAudioAssets(nil), primary...), layers...) {
		if _, ok := seen[asset.AssetID]; ok {
			continue
		}
		seen[asset.AssetID] = struct{}{}
		out = append(out, asset)
	}
	return out
}
