// Package rustexec — audio_gate_vo_bgm_e2e_test.go is listen gate A:
// voiceover + looped BGM on a 25s timeline with explicit, audible gains
// (the built-in defaults would sit near -30dB and be pointless to listen
// to). Go compiles the sealed plan (loop expansion, fades, ducking), the
// real Rust/FFmpeg renderer produces the master, and the master is
// certified by frequency band before being persisted for manual listening.
package rustexec

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestListenGateVOPlusBGM25s: one voiceover speech window [0,15) over a
// 10s BGM looped across the whole 25s timeline. BGM base -18dB, ducked to
// -28dB under the narration, fade-in 800ms, fade-out 1000ms.
func TestListenGateVOPlusBGM25s(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}
	ffprobePath := resolveFFprobeBinary(ffmpegPath)

	// Real assets: BGM at 60Hz (~4 octaves below the 1000Hz voiceover) so
	// every band measurement isolates its source.
	assetsDir := t.TempDir()
	bgmPath := filepath.Join(assetsDir, "bgm_a.wav")
	generateToneAsset(t, ffmpegPath, bgmPath, 60, 10)
	voPath := filepath.Join(assetsDir, "vo_a.wav")
	generateToneAsset(t, ffmpegPath, voPath, 1000, 15)
	voDur := probeDurationUS(t, ffprobePath, voPath)
	bgmDur := probeDurationUS(t, ffprobePath, bgmPath)

	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 25_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene-a", Index: 0, TimelineStartUS: 0, DurationUS: 25_000_000,
			AudioIntents: []audio.AudioIntent{{
				Mode:               audio.AudioVoiceover,
				VoiceoverAssetID:   "vo-a",
				SourceDurationUS:   voDur,
				TimelineOffsetUS:   0,
				TimelineDurationUS: 25_000_000,
			}},
		}},
	}
	if err := timeline.Validate(); err != nil {
		t.Fatalf("gate A canonical timeline is invalid: %v", err)
	}

	bgmIntents := []scriptpkg.BackgroundMusicIntent{{
		AssetID:            "bgm-a",
		StartMS:            0,
		End:                &scriptpkg.AudioTimelineEnd{VideoEnd: true},
		Loop:               true,
		GainDB:             -18,
		FadeInMS:           800,
		FadeOutMS:          1000,
		DuckUnderVoiceover: true,
		DuckGainDB:         -28,
		DuckAttackMS:       120,
		DuckReleaseMS:      350,
	}}
	source := mapAudioAssetSource{assets: map[string]audio.ResolvedAudioAsset{
		"bgm-a": {AssetID: "bgm-a", Path: bgmPath, DurationUS: bgmDur},
	}}

	compileStarted := time.Now()
	result, err := scripts.CompileAudioWithIntents(context.Background(), timeline, audio.DefaultAudioProfile(), "voiceover_with_ducked_clip", bgmIntents, nil, source)
	if err != nil {
		t.Fatalf("compile gate A audio intents: %v", err)
	}
	compileMS := time.Since(compileStarted).Milliseconds()
	plan := result.Plan

	// ── Plan gates BEFORE listening ──
	vo := trackEvents(plan, audio.TrackVoiceover)
	if len(vo) != 1 {
		t.Fatalf("voiceover events = %d, want 1", len(vo))
	}
	sfx := trackEvents(plan, audio.TrackSFX)
	if len(sfx) != 0 {
		t.Fatalf("sfx events = %+v, want none in gate A", sfx)
	}
	bgm := trackEvents(plan, audio.TrackBGM)
	wantBGM := []audio.AudioEvent{
		{EventID: "bgm-0", Type: audio.EventBGM, AssetID: "bgm-a", TimelineStartUS: 0, DurationUS: 10_000_000, SourceInUS: 0, SourceDurationUS: 10_000_000, GainDB: -18},
		{EventID: "bgm-1", Type: audio.EventBGM, AssetID: "bgm-a", TimelineStartUS: 10_000_000, DurationUS: 10_000_000, SourceInUS: 0, SourceDurationUS: 10_000_000, GainDB: -18},
		{EventID: "bgm-2", Type: audio.EventBGM, AssetID: "bgm-a", TimelineStartUS: 20_000_000, DurationUS: 5_000_000, SourceInUS: 0, SourceDurationUS: 5_000_000, GainDB: -18},
	}
	if len(bgm) != len(wantBGM) {
		t.Fatalf("bgm events = %+v, want %d loop events (last truncated on 25s)", bgm, len(wantBGM))
	}
	for i := range wantBGM {
		if bgm[i] != wantBGM[i] {
			t.Fatalf("bgm[%d] = %+v\nwant     %+v", i, bgm[i], wantBGM[i])
		}
	}
	if len(plan.Automation) != 2 {
		t.Fatalf("automation = %+v, want 1 fade + 1 duck entry", plan.Automation)
	}
	fade := plan.Automation[0]
	if fade.TargetTrackID != "bgm" || fade.StartUS != 0 || fade.EndUS != 25_000_000 || fade.AttackUS != 800_000 || fade.ReleaseUS != 1_000_000 {
		t.Fatalf("fade automation = %+v", fade)
	}
	duck := plan.Automation[1]
	if duck.TargetTrackID != "bgm" || duck.TriggerTrackID != "voiceover" || duck.StartUS != 0 || duck.EndUS != 15_000_000 || duck.GainDB != -28 {
		t.Fatalf("duck automation = %+v, want [0,15s) at -28dB", duck)
	}
	if plan.DurationUS != 25_000_000 || plan.MixPolicy != audio.MixVoiceoverWithDuckedClip || plan.PlanSHA256 == "" {
		t.Fatalf("plan contract broken: duration=%d policy=%q sealed=%v", plan.DurationUS, plan.MixPolicy, plan.PlanSHA256 != "")
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("sealed gate A plan is invalid: %v", err)
	}

	// ── Real render + persistence for manual listening ──
	renderAssets := append(result.Assets, audio.ResolvedAudioAsset{AssetID: "vo-a", Path: voPath})
	master := renderGateAudio(t, musclesPath, ffmpegPath, plan, renderAssets, "A_vo_bgm")
	certifyGateMaster(t, ffprobePath, master.Path, 25_000_000)
	logGateTiming(t, "A_vo_bgm", compileMS, master)

	// ── Frequency certification of the master ──
	// Voiceover present while speaking, absent after.
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 0, 15, 800, 1200); got <= -30 {
		t.Fatalf("voiceover tone missing in [0,15): %.2f dB", got)
	}
	if got := bandMeanVolumeDB(t, ffmpegPath, master.Path, 15, 25, 800, 1200); got >= -25 {
		t.Fatalf("voiceover must be absent in [15,25): mean %.2f dB", got)
	}
	// BGM loop restarts at 10s and the truncated third event still covers
	// [20,25) — music up to the video end, no brutal cut.
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 11, 14, 30, 90); got <= -40 {
		t.Fatalf("bgm loop event 2 missing in [11,14): %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 21, 23.5, 30, 90); got <= -40 {
		t.Fatalf("bgm loop event 3 (truncated tail) missing in [21,23.5): %.2f dB", got)
	}
	// Ducking: while the narration speaks the music sits ~10dB below its
	// base level.
	ducked := bandMeanVolumeDB(t, ffmpegPath, master.Path, 2, 13, 30, 90)
	base := bandMeanVolumeDB(t, ffmpegPath, master.Path, 16, 23, 30, 90)
	if ducked >= base-4 {
		t.Fatalf("bgm ducking not effective: mean during VO %.2f dB vs base %.2f dB", ducked, base)
	}
	// Fade-in: the first 300ms ramp well below the steady ducked level.
	fadeIn := bandMeanVolumeDB(t, ffmpegPath, master.Path, 0, 0.3, 30, 90)
	steadyDucked := bandMeanVolumeDB(t, ffmpegPath, master.Path, 2, 4, 30, 90)
	if fadeIn >= steadyDucked-3 {
		t.Fatalf("bgm fade-in not effective: mean [0,0.3) %.2f dB vs steady %.2f dB", fadeIn, steadyDucked)
	}
	// Fade-out: the last 450ms ramp well below the pre-fade base level.
	fadeOut := bandMeanVolumeDB(t, ffmpegPath, master.Path, 24.55, 25, 30, 90)
	preFade := bandMeanVolumeDB(t, ffmpegPath, master.Path, 22, 23.5, 30, 90)
	if fadeOut >= preFade-3 {
		t.Fatalf("bgm fade-out not effective: mean [24.55,25) %.2f dB vs pre-fade %.2f dB", fadeOut, preFade)
	}
}
