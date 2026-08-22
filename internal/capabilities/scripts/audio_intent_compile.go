// Package scriptgeneration — audio_intent_compile.go is the single
// canonical entry point that turns the run's audio intent block into the
// sealed compiled audio plan.
//
// Pipeline (no parallel path — every component funnels into
// audio.CompileWithLayers):
//
//	GenerateRequest intents (BGM + SFX, asset_ids only)
//	    ↓ AudioAssetResolver → ResolvedAudioAssets (path + certified duration)
//	    ↓ BackgroundMusicResolver → ResolvedBGM windows
//	    ↓ AudioLoopExpander → BGM AudioLayers (deterministic loop events)
//	    ↓ AudioIntentResolver → ResolvedSFX → SFX AudioLayers (absolute + trims)
//	    ↓ AudioAutomationCompiler → fades + ducking automation
//	    ↓ audio.CompileWithLayersAndPolicy
//	    ↓ sealed CompiledAudioPlan (+ ResolvedAudioAssets for the renderer)
//
// Go decides every timing fact; Rust only executes the sealed plan.
package scriptgeneration

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// AudioIntentCompileResult is the typed outcome of the intent compile:
// the sealed plan plus the resolved asset table the renderer consumes
// alongside it.
type AudioIntentCompileResult struct {
	Plan   audio.CompiledAudioPlan
	Assets audio.ResolvedAudioAssets
}

// CompileAudioWithIntents compiles the audio intent block (BGM + SFX)
// into the sealed CompiledAudioPlan. The canonical timeline still owns
// every primary event offset; the intents only add layers and automation.
//
// Fail-closed: an unresolved asset, a BGM whose source duration is
// unknown (loop expansion is not deterministic without it), an SFX
// without an explicit duration whose source duration is unknown, or a
// source trim that overruns the certified source length all fail the
// compile — the run never renders a partial or guessed plan.
func CompileAudioWithIntents(
	ctx context.Context,
	timeline audio.CanonicalTimeline,
	profile audio.CanonicalAudioProfile,
	policy audio.AudioMixPolicy,
	bgmIntents []scriptpkg.BackgroundMusicIntent,
	sfxIntents []scriptpkg.SoundEffectIntent,
	source AudioAssetSource,
) (AudioIntentCompileResult, error) {
	fail := func(err error) (AudioIntentCompileResult, error) {
		return AudioIntentCompileResult{}, err
	}

	// 1. Asset resolution: asset_ids → paths + certified durations.
	assetResolver, err := NewAudioAssetResolver(source)
	if err != nil {
		return fail(err)
	}
	assets, err := assetResolver.Resolve(ctx, bgmIntents, sfxIntents)
	if err != nil {
		return fail(err)
	}
	durationByID := make(map[string]int64, len(assets))
	for _, a := range assets {
		durationByID[a.AssetID] = a.DurationUS
	}

	// 2. BGM: windows → loop expansion (deterministic events, last one
	// truncated exactly on the window end).
	resolvedBGM, err := NewBackgroundMusicResolver().Resolve(timeline, bgmIntents)
	if err != nil {
		return fail(err)
	}
	expander := NewAudioLoopExpander()
	var bgmLayers []audio.AudioLayer
	for _, layer := range resolvedBGM {
		expanded, err := expander.Expand(layer, durationByID[layer.AssetID])
		if err != nil {
			return fail(fmt.Errorf("expand bgm %s: %w", layer.AssetID, err))
		}
		bgmLayers = append(bgmLayers, expanded...)
	}

	// 3. SFX: scene-relative commands → absolute placements → layers with
	// source trims. An SFX without an explicit duration is sized from the
	// certified source duration; without either, the event is not
	// deterministic and fails closed.
	resolvedSFX, err := NewAudioIntentResolver().ResolveSoundEffects(timeline, sfxIntents)
	if err != nil {
		return fail(err)
	}
	var sfxLayers []audio.AudioLayer
	for _, s := range resolvedSFX {
		dur := s.DurationUS
		if dur <= 0 {
			dur = durationByID[s.AssetID]
			if dur <= 0 {
				return fail(fmt.Errorf("sfx %s has no explicit duration and its source duration is unknown", s.AssetID))
			}
		}
		if s.TimelineStartUS > timeline.DurationUS-dur {
			return fail(fmt.Errorf("sfx %s placement [%d,%d) exceeds the %dus timeline", s.AssetID, s.TimelineStartUS, s.TimelineStartUS+dur, timeline.DurationUS))
		}
		if srcDur := durationByID[s.AssetID]; srcDur > 0 && s.SourceInUS > srcDur-dur {
			return fail(fmt.Errorf("sfx %s source trim [%d,%d) overruns the %dus source", s.AssetID, s.SourceInUS, s.SourceInUS+dur, srcDur))
		}
		sfxLayers = append(sfxLayers, audio.AudioLayer{
			AssetID:         s.AssetID,
			TimelineStartUS: s.TimelineStartUS,
			DurationUS:      dur,
			SourceInUS:      s.SourceInUS,
			GainDB:          s.GainDB,
		})
	}

	// 4. Automation: fades first, then ducking under voiceover — both
	// deterministic and both on the canonical "bgm" track.
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

	// 5. Canonical compile: the ONLY plan builder. The mix policy is
	// recorded on the plan so the mixer and renderer consume the same
	// editorial decision.
	plan, err := audio.CompileWithLayersAndPolicy(timeline, profile, bgmLayers, sfxLayers, automation, policy)
	if err != nil {
		return fail(err)
	}
	return AudioIntentCompileResult{Plan: plan, Assets: assets}, nil
}

// CompileCanonicalAudioPlanAudioOnlyWithIntents is the audio-compile entry
// point when a run carries a BGM/SFX intent block. It builds the same
// VO-governed canonical timeline + primary (voiceover/original-clip) assets
// as CompileCanonicalAudioPlanAudioOnly, then funnels the intents through
// the canonical pipeline — AudioAssetResolver → BackgroundMusicResolver →
// AudioLoopExpander → AudioIntentResolver → AudioAutomationCompiler — into
// audio.CompileWithLayers (via CompileAudioWithIntents). The primary and
// BGM/SFX asset tables are merged into one renderer input.
//
// Fail-closed: a nil source, an unresolved asset, a BGM without a certified
// duration, or an SFX that overruns its source fails the whole compile — the
// run never renders a partial or guessed layered plan.
func CompileCanonicalAudioPlanAudioOnlyWithIntents(
	ctx context.Context,
	result GenerateResult,
	language Language,
	profile audio.CanonicalAudioProfile,
	source AudioAssetSource,
	policy audio.AudioMixPolicy,
	bgm []scriptpkg.BackgroundMusicIntent,
	sfx []scriptpkg.SoundEffectIntent,
) (audio.CanonicalTimeline, audio.CompiledAudioPlan, audio.ResolvedAudioAssets, AudioCompileTimings, error) {
	// Audio-only narration runs may be compiled before clip materialization.
	// With VOICEOVER_ONLY there is deliberately no dependency on a local clip
	// audio path: the master is narration + optional BGM, while the rendered
	// MP4 clips keep their own source audio contract independently.
	if policy == audio.MixVoiceoverOnly {
		result = resultWithoutClipAudioIntents(result)
	}
	timeline, primaryAssets, timings, err := buildCanonicalTimelineAndPrimaryAssets(result, language, false)
	if err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, err
	}
	planStarted := time.Now()
	compiled, err := CompileAudioWithIntents(ctx, timeline, profile, policy, bgm, sfx, source)
	timings.AudioPlanCompileMS = time.Since(planStarted).Milliseconds()
	if err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, err
	}
	return timeline, compiled.Plan, mergeResolvedAudioAssets(primaryAssets, compiled.Assets), timings, nil
}

func resultWithoutClipAudioIntents(result GenerateResult) GenerateResult {
	result.ResolvedScenes = nil
	result.Scenes = append([]Scene(nil), result.Scenes...)
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		intents := make([]audio.AudioIntent, 0, len(scene.AudioIntents))
		for _, intent := range scene.AudioIntents {
			if intent.Mode != audio.AudioClip {
				intents = append(intents, intent)
			}
		}
		scene.AudioIntents = intents
		if scene.Audio.Mode == audio.AudioClip {
			scene.Audio.Mode = audio.AudioVoiceover
		}
	}
	return result
}

// mergeResolvedAudioAssets merges the primary (voiceover/original-clip) asset
// table with the resolved BGM/SFX table, preserving the primary-first order
// and de-duplicating on asset_id. The two tables are disjoint by construction
// (scene-bound VO/clip ids vs intent asset ids); the dedup is a safety net.
func mergeResolvedAudioAssets(primary, layers audio.ResolvedAudioAssets) audio.ResolvedAudioAssets {
	if len(layers) == 0 {
		return primary
	}
	out := make(audio.ResolvedAudioAssets, 0, len(primary)+len(layers))
	seen := make(map[string]struct{}, len(primary)+len(layers))
	for _, asset := range append(append(audio.ResolvedAudioAssets(nil), primary...), layers...) {
		if _, ok := seen[asset.AssetID]; ok {
			continue
		}
		seen[asset.AssetID] = struct{}{}
		out = append(out, asset)
	}
	return out
}
