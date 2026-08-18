// Package scriptgeneration — runner_audio_intent_test.go: regression guards
// for the audio-intent pipeline wired into the audio-compile phase. A run
// carrying a BGM/SFX intent block must compile the layered plan through the
// canonical pipeline (asset resolution → BGM loop expansion → SFX placement
// → automation) and fail closed when the asset resolver is not wired.
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// intentRunnerSource returns a deterministic AudioAssetSource with certified
// durations: a 2s BGM and a 0.5s SFX.
func intentRunnerSource() *fakeAudioAssetSource {
	source := newFakeAudioAssetSource(map[string]string{
		"bgm_2s": "/m/bgm.m4a",
		"sfx_1":  "/m/sfx.m4a",
	})
	source.assets["bgm_2s"] = capabilityaudio.ResolvedAudioAsset{AssetID: "bgm_2s", Path: "/m/bgm.m4a", DurationUS: 2_000_000}
	source.assets["sfx_1"] = capabilityaudio.ResolvedAudioAsset{AssetID: "sfx_1", Path: "/m/sfx.m4a", DurationUS: 500_000}
	return source
}

// TestRunnerAudioIntentsCompileLayeredPlan certifies the full production
// flow: a COMBINED_TIMELINE run with a BGM/SFX intent block persists an
// audio plan whose BGM is loop-expanded by Go (deterministic events), whose
// SFX is placed absolutely, and whose BGM ducking automation is present —
// the renderer is handed a fully-resolved plan, never the raw intents.
func TestRunnerAudioIntentsCompileLayeredPlan(t *testing.T) {
	runner, repo, textGen, _, voiceoverGen, _, _ := newTestRunner()
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})
	runner.SetAudioAssetSource(intentRunnerSource())

	// One deterministic 3s scene whose voiceover is synthesized at exactly 3s,
	// so the narration-driven scene duration makes the timeline 3s and the
	// BGM loop expansion is fully predictable. (The stub text generator clones
	// scenes without the pre-filled Voiceover map, so we drive the duration
	// through the stub voiceover generator instead.)
	voiceoverGen.ref.Duration = 3.0
	textGen.scenes = []Scene{{
		ID:           "scene-0",
		Index:        0,
		Audio:        capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioVoiceover}},
		Text:         map[Language]string{"en": "A scene with narration, looped music and one effect."},
	}}

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.SourceLanguage = "en"
	req.Languages = []Language{"en"}
	req.BackgroundMusic = []scriptpkg.BackgroundMusicIntent{{
		AssetID:            "bgm_2s",
		Loop:               true,
		GainDB:             -24,
		DuckUnderVoiceover: true,
		DuckGainDB:         -30,
	}}
	req.SoundEffects = []scriptpkg.SoundEffectIntent{{AssetID: "sfx_1", AtMS: 1000, GainDB: -8}}

	runID := "run-audio-intents-layered"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "audio-intent run must complete: %s", final.ErrorMessage)

	res := final.Result
	require.NotNil(t, res, "result must be present")
	require.NotNil(t, res.AudioPlan, "audio plan must be persisted")
	require.Equal(t, capabilityaudio.MixVoiceoverWithDuckedClip, res.AudioPlan.MixPolicy, "default mix policy must be the ducked clip policy")

	// BGM loop expansion: the 2s source covers the 3s timeline as two
	// deterministic events [0,2s) and [2s,3s) — Go decided the loop, the
	// plan carries it, Rust never invents it.
	bgm := eventsForRole(*res.AudioPlan, capabilityaudio.TrackBGM)
	require.Len(t, bgm, 2, "BGM must be loop-expanded into 2 deterministic events")
	require.Equal(t, int64(0), bgm[0].TimelineStartUS)
	require.Equal(t, int64(2_000_000), bgm[0].DurationUS)
	require.Equal(t, int64(2_000_000), bgm[1].TimelineStartUS)
	require.Equal(t, int64(1_000_000), bgm[1].DurationUS, "the last BGM event must be truncated on the timeline end")

	// SFX placement: one absolute event at 1s, sized from the certified source.
	sfx := eventsForRole(*res.AudioPlan, capabilityaudio.TrackSFX)
	require.Len(t, sfx, 1, "SFX must be placed as one event")
	require.Equal(t, int64(1_000_000), sfx[0].TimelineStartUS)
	require.Equal(t, int64(500_000), sfx[0].DurationUS)

	// Voiceover preserved: one event for the single scene.
	require.Len(t, eventsForRole(*res.AudioPlan, capabilityaudio.TrackVoiceover), 1, "voiceover must be preserved")

	// BGM ducking under voiceover is compiled into automation (one entry per
	// speech window overlapping the BGM layer).
	require.NotEmpty(t, res.AudioPlan.Automation, "BGM ducking automation must be part of the layered plan")
	for _, automation := range res.AudioPlan.Automation {
		require.Equal(t, "bgm", automation.TargetTrackID, "ducking must target the bgm track")
		require.Equal(t, "voiceover", automation.TriggerTrackID, "ducking must be triggered by voiceover")
	}
}

// TestRunnerAudioIntentsWithoutResolverFailsClosed certifies that a run
// carrying BGM/SFX intents with no wired asset resolver fails in the
// audio-compile phase instead of silently dropping the intents.
func TestRunnerAudioIntentsWithoutResolverFailsClosed(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.SourceLanguage = "en"
	req.Languages = []Language{"en"}
	req.BackgroundMusic = []scriptpkg.BackgroundMusicIntent{{AssetID: "bgm_2s", Loop: true}}

	runID := "run-audio-intents-no-resolver"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusFailed, final.Status, "audio-intent run without a resolver must fail")
	require.Equal(t, StageCompilingAudio, final.FailedStage, "failure must be in the audio-compile phase")
	require.Contains(t, final.ErrorMessage, "audio asset resolver", "failure must name the missing resolver")
}

// TestRunnerAudioIntentsWithoutIntentsKeepsLegacyPath certifies the absence
// of an intent block keeps the legacy primary-only compile path: no BGM/SFX
// tracks are added and no resolver is required.
func TestRunnerAudioIntentsWithoutIntentsKeepsLegacyPath(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.SourceLanguage = "en"
	req.Languages = []Language{"en"}

	runID := "run-no-intents-legacy"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "no-intent run must complete: %s", final.ErrorMessage)

	res := final.Result
	require.NotNil(t, res.AudioPlan, "audio plan must be persisted")
	require.Empty(t, eventsForRole(*res.AudioPlan, capabilityaudio.TrackBGM), "no-intent plan must carry no BGM")
	require.Empty(t, eventsForRole(*res.AudioPlan, capabilityaudio.TrackSFX), "no-intent plan must carry no SFX")
}
