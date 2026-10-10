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
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernelaudio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// AudioIntentCompileResult is the typed outcome of the intent compile:
// the sealed plan plus the resolved asset table the renderer consumes
// alongside it.
type AudioIntentCompileResult struct {
	Plan                audio.CompiledAudioPlan
	Assets              audio.ResolvedAudioAssets
	AudioAssetResolveMS int64
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
	assetResolveStarted := time.Now()
	assetResolver, err := NewAudioAssetResolver(source)
	if err != nil {
		return fail(err)
	}
	assets, err := assetResolver.Resolve(ctx, bgmIntents, sfxIntents)
	if err != nil {
		return fail(err)
	}
	// This timing is returned through the canonical compile result so the
	// resolver boundary remains distinct from plan compilation.
	assetResolveMS := time.Since(assetResolveStarted).Milliseconds()
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
	return AudioIntentCompileResult{Plan: plan, Assets: assets, AudioAssetResolveMS: assetResolveMS}, nil
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
	randomSFXOnClipStart ...bool,
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
	if len(randomSFXOnClipStart) > 0 && randomSFXOnClipStart[0] {
		sfx = append(append([]scriptpkg.SoundEffectIntent(nil), sfx...), randomClipStartSFXIntents(timeline)...)
	}
	planStarted := time.Now()
	compiled, err := CompileAudioWithIntents(ctx, timeline, profile, policy, bgm, sfx, source)
	timings.AudioPlanCompileMS = time.Since(planStarted).Milliseconds()
	timings.AudioAssetResolveMS = compiled.AudioAssetResolveMS
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
			if intent.Mode != audio.AudioClip || intent.ProtectedOriginalAudio {
				intents = append(intents, intent)
			}
		}
		scene.AudioIntents = intents
		if scene.Audio.Mode == audio.AudioClip && !scene.Audio.ProtectedOriginalAudio {
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

// finalJobAudioInput projects the canonical mix onto the remote final-job
// handoff: TTS, BGM and SFX pass through, and the original clip audio is
// restored at FinalJobRestoredClipGainDB instead of being dropped. History:
// this projection used to remove every AudioClip intent, which delivered
// videos with silent clip audio (2026-09-30 Milton incident) — the source
// speech was audible only in the local lane while the remote master mixed
// narration + music alone. The restored track ducks like any clip track:
// applyMixPolicy only deepens non-protected events toward the active duck
// gain while speech plays.
func finalJobAudioInput(result GenerateResult, language Language) GenerateResult {
	result.Scenes = append([]Scene(nil), result.Scenes...)
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		// Fixed-media scenes normally require their source clip's original
		// audio. The remote final-job path deliberately hands that clip to 51,
		// so the local audio projection treats the slot as generated silence
		// or narration while retaining its explicit duration.
		if scene.ExecutionMode.IsFixedMedia() {
			scene.ExecutionMode = scriptpkg.SceneExecutionGenerated
		}
		intents := scene.AudioIntents
		if len(intents) == 0 && scene.Audio.Mode != "" {
			intents = []capabilityaudio.AudioIntent{scene.Audio}
		}
		// Ordinary scenes bound to an editorial clip must contribute that
		// clip's source audio to the same canonical master as their narration.
		// Historically only fixed intro/outro sections carried an AudioClip
		// intent, so intermediate clip scenes could reach 51 with narration
		// alone (or with an inconsistent source track inherited downstream).
		// Stock-selected scenes remain visual-only, EXCEPT for clips the caller
		// explicitly marked as used-as-stock: those keep the clip audio at full
		// original volume because the caller asked for that clip to be HEARD.
		stockClipIDs := sceneStockClipIDs(*scene)
		if !scene.ExecutionMode.IsFixedMedia() && scene.Stock == nil && scene.Clip != nil && !hasClipAudioIntent(intents, scene.Clip.ID) {
			if sourceInUS, sourceDurationUS, ok := finalJobClipAudioWindow(scene.Clip); ok {
				intents = append(intents, capabilityaudio.AudioIntent{
					Mode: capabilityaudio.AudioClip, ClipAssetID: scene.Clip.ID,
					SourceInUS: sourceInUS, SourceDurationUS: sourceDurationUS,
					TimelineOffsetUS: 0, TimelineDurationUS: sourceDurationUS,
					UseOriginalAudio: true, GainDB: kernelaudio.FinalJobRestoredClipGainDB,
				})
			}
		}
		// A stock-marked clip is an explicit editorial request to hear that
		// clip: its original audio joins the master at full volume even when
		// the scene's visual comes from a stock folder and even when the run's
		// global policy is VOICEOVER_ONLY.
		for _, clip := range sceneClips(*scene) {
			if clip == nil || !clip.AsStock || hasClipAudioIntent(intents, clip.ID) {
				continue
			}
			if sourceInUS, sourceDurationUS, ok := finalJobClipAudioWindow(clip); ok {
				intents = append(intents, capabilityaudio.AudioIntent{
					Mode: capabilityaudio.AudioClip, ClipAssetID: clip.ID,
					SourceInUS: sourceInUS, SourceDurationUS: sourceDurationUS,
					TimelineOffsetUS: 0, TimelineDurationUS: sourceDurationUS,
					UseOriginalAudio: true, ProtectedOriginalAudio: true, GainDB: 0,
				})
			}
		}
		filtered := make([]capabilityaudio.AudioIntent, 0, len(intents)+1)
		hasVoiceover := false
		for _, intent := range intents {
			if intent.Mode == capabilityaudio.AudioClip {
				// The clip's original audio reaches the remote master: keep the
				// intent and stamp the restored-mix gain. The mix policy still
				// ducks it under speech; the compiler never touches an explicit
				// non-zero GainDB. A stock-marked clip is the exception: the
				// caller asked for it AS STOCK, so it stays at full original
				// volume and keeps its protection against VO-only removal and
				// ducking.
				restored := intent
				if _, isStock := stockClipIDs[intent.ClipAssetID]; isStock {
					restored.ProtectedOriginalAudio = true
					restored.GainDB = 0
				} else {
					restored.GainDB = kernelaudio.FinalJobRestoredClipGainDB
				}
				filtered = append(filtered, restored)
				continue
			}
			if intent.Mode == capabilityaudio.AudioVoiceover {
				hasVoiceover = true
			}
			filtered = append(filtered, intent)
		}
		if !hasVoiceover {
			if voiceover, ok := scene.Voiceover[language]; ok && voiceover.ID != "" {
				filtered = append(filtered, capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover, VoiceoverAssetID: voiceover.ID})
			}
		}
		if len(filtered) == 0 {
			filtered = append(filtered, capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioSilence})
		}
		scene.AudioIntents = filtered
		scene.Audio = filtered[0]
	}
	return result
}

func hasClipAudioIntent(intents []capabilityaudio.AudioIntent, clipID string) bool {
	for _, intent := range intents {
		if intent.Mode == capabilityaudio.AudioClip && intent.ClipAssetID == clipID {
			return true
		}
	}
	return false
}

func finalJobClipAudioWindow(clip *ClipReference) (sourceInUS, durationUS int64, ok bool) {
	if clip == nil || strings.TrimSpace(clip.ID) == "" || clip.SourceInMS < 0 {
		return 0, 0, false
	}
	sourceInUS = clip.SourceInMS * 1000
	if clip.SourceOutMS > clip.SourceInMS {
		durationUS = (clip.SourceOutMS - clip.SourceInMS) * 1000
	} else if total := clip.AssetDuration().DurationUS; total > sourceInUS {
		durationUS = total - sourceInUS
	}
	return sourceInUS, durationUS, durationUS > 0
}
