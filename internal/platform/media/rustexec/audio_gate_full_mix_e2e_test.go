// Package rustexec — audio_gate_full_mix_e2e_test.go is listen gate C, the
// definitive combination: voiceover + looped BGM + three sound effects +
// ducking + fades on a 30s three-scene timeline. The second test certifies
// the scene-relative placement path (scene_id + anchor + offset_ms) against
// the same timeline: start+500ms, middle, end−300ms.
package rustexec

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// gateCTimeline is the 30s three-scene timeline (10s each) whose certified
// voiceover speech windows are [0,8s), [10s,18s) and [20s,28s).
func gateCTimeline(voDurations map[string]int64) audio.CanonicalTimeline {
	scenes := []struct {
		id        string
		voAssetID string
		start     int64
	}{
		{"scene-c1", "vo-c1", 0},
		{"scene-c2", "vo-c2", 10_000_000},
		{"scene-c3", "vo-c3", 20_000_000},
	}
	segments := make([]audio.TimelineSegment, 0, len(scenes))
	for i, s := range scenes {
		segments = append(segments, audio.TimelineSegment{
			ID:              s.id,
			Index:           i,
			TimelineStartUS: s.start,
			DurationUS:      10_000_000,
			AudioIntents: []audio.AudioIntent{{
				Mode:               audio.AudioVoiceover,
				VoiceoverAssetID:   s.voAssetID,
				SourceDurationUS:   voDurations[s.voAssetID],
				TimelineOffsetUS:   0,
				TimelineDurationUS: 10_000_000,
			}},
		})
	}
	return audio.CanonicalTimeline{Version: audio.TimelineVersion, DurationUS: 30_000_000, Segments: segments}
}

// gateCBGM is the shared BGM intent of gate C: a 12s source looped over the
// whole 30s video with fades and ducking under the narration.
func gateCBGM() []scriptpkg.BackgroundMusicIntent {
	return []scriptpkg.BackgroundMusicIntent{{
		AssetID:            "bgm-c",
		StartMS:            0,
		End:                &scriptpkg.AudioTimelineEnd{VideoEnd: true},
		Loop:               true,
		GainDB:             -20,
		FadeInMS:           500,
		FadeOutMS:          1000,
		DuckUnderVoiceover: true,
		DuckGainDB:         -30,
		DuckAttackMS:       120,
		DuckReleaseMS:      350,
	}}
}

// TestListenGateFullMix30s: VO + BGM(loop) + 3 SFX(absolute) + ducking +
// fades, mastered through the real Rust/FFmpeg renderer.
func TestListenGateFullMix30s(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}
	ffprobePath := resolveFFprobeBinary(ffmpegPath)

	assetsDir := t.TempDir()
	bgmPath := filepath.Join(assetsDir, "bgm_c.wav") // 60Hz, 12s
	generateToneAsset(t, ffmpegPath, bgmPath, 60, 12)
	voPaths := make(map[string]string, 3)
	voDurations := make(map[string]int64, 3)
	for _, id := range []string{"vo-c1", "vo-c2", "vo-c3"} {
		path := filepath.Join(assetsDir, id+".wav") // 1000Hz, 8s each
		generateToneAsset(t, ffmpegPath, path, 1000, 8)
		voPaths[id] = path
		voDurations[id] = probeDurationUS(t, ffprobePath, path)
	}
	sfxSpecs := []struct {
		id        string
		freq      float64
		durationS float64
	}{
		{"sfx-c-whoosh", 3000, 2}, // >= explicit duration 900ms
		{"sfx-c-impact", 5000, 0.5},
		{"sfx-c-boom", 8000, 0.3},
	}
	sfxPaths := make(map[string]string, len(sfxSpecs))
	sfxDurations := make(map[string]int64, len(sfxSpecs))
	for _, spec := range sfxSpecs {
		path := filepath.Join(assetsDir, spec.id+".wav")
		generateToneAsset(t, ffmpegPath, path, spec.freq, spec.durationS)
		sfxPaths[spec.id] = path
		sfxDurations[spec.id] = probeDurationUS(t, ffprobePath, path)
	}
	bgmDur := probeDurationUS(t, ffprobePath, bgmPath)

	timeline := gateCTimeline(voDurations)
	if err := timeline.Validate(); err != nil {
		t.Fatalf("gate C canonical timeline is invalid: %v", err)
	}
	sfxIntents := []scriptpkg.SoundEffectIntent{
		{AssetID: "sfx-c-whoosh", AtMS: 5_000, SourceInMS: 0, DurationMS: 900, GainDB: -8},
		{AssetID: "sfx-c-impact", AtMS: 15_000, GainDB: -6},
		{AssetID: "sfx-c-boom", AtMS: 24_000, GainDB: -5},
	}
	source := mapAudioAssetSource{assets: map[string]audio.ResolvedAudioAsset{
		"bgm-c":        {AssetID: "bgm-c", Path: bgmPath, DurationUS: bgmDur},
		"sfx-c-whoosh": {AssetID: "sfx-c-whoosh", Path: sfxPaths["sfx-c-whoosh"], DurationUS: sfxDurations["sfx-c-whoosh"]},
		"sfx-c-impact": {AssetID: "sfx-c-impact", Path: sfxPaths["sfx-c-impact"], DurationUS: sfxDurations["sfx-c-impact"]},
		"sfx-c-boom":   {AssetID: "sfx-c-boom", Path: sfxPaths["sfx-c-boom"], DurationUS: sfxDurations["sfx-c-boom"]},
	}}

	compileStarted := time.Now()
	result, err := scripts.CompileAudioWithIntents(context.Background(), timeline, audio.DefaultAudioProfile(), "voiceover_with_ducked_clip", gateCBGM(), sfxIntents, source)
	if err != nil {
		t.Fatalf("compile gate C audio intents: %v", err)
	}
	compileMS := time.Since(compileStarted).Milliseconds()
	plan := result.Plan

	// ── Plan gates BEFORE listening ──
	vo := trackEvents(plan, audio.TrackVoiceover)
	if len(vo) != 3 {
		t.Fatalf("voiceover events = %d, want 3", len(vo))
	}
	bgm := trackEvents(plan, audio.TrackBGM)
	wantBGM := []audio.AudioEvent{
		{EventID: "bgm-0", Type: audio.EventBGM, AssetID: "bgm-c", TimelineStartUS: 0, DurationUS: 12_000_000, SourceInUS: 0, SourceDurationUS: 12_000_000, GainDB: -20},
		{EventID: "bgm-1", Type: audio.EventBGM, AssetID: "bgm-c", TimelineStartUS: 12_000_000, DurationUS: 12_000_000, SourceInUS: 0, SourceDurationUS: 12_000_000, GainDB: -20},
		{EventID: "bgm-2", Type: audio.EventBGM, AssetID: "bgm-c", TimelineStartUS: 24_000_000, DurationUS: 6_000_000, SourceInUS: 0, SourceDurationUS: 6_000_000, GainDB: -20},
	}
	if len(bgm) != len(wantBGM) {
		t.Fatalf("bgm events = %+v, want %d loop events (last truncated on 30s)", bgm, len(wantBGM))
	}
	for i := range wantBGM {
		if bgm[i] != wantBGM[i] {
			t.Fatalf("bgm[%d] = %+v\nwant     %+v", i, bgm[i], wantBGM[i])
		}
	}
	sfx := trackEvents(plan, audio.TrackSFX)
	wantSFX := []audio.AudioEvent{
		{EventID: "sfx-0", Type: audio.EventSFX, AssetID: "sfx-c-whoosh", TimelineStartUS: 5_000_000, DurationUS: 900_000, SourceInUS: 0, SourceDurationUS: 900_000, GainDB: -8},
		{EventID: "sfx-1", Type: audio.EventSFX, AssetID: "sfx-c-impact", TimelineStartUS: 15_000_000, DurationUS: 500_000, SourceInUS: 0, SourceDurationUS: 500_000, GainDB: -6},
		{EventID: "sfx-2", Type: audio.EventSFX, AssetID: "sfx-c-boom", TimelineStartUS: 24_000_000, DurationUS: 300_000, SourceInUS: 0, SourceDurationUS: 300_000, GainDB: -5},
	}
	if len(sfx) != len(wantSFX) {
		t.Fatalf("sfx events = %+v, want %d", sfx, len(wantSFX))
	}
	for i := range wantSFX {
		if sfx[i] != wantSFX[i] {
			t.Fatalf("sfx[%d] = %+v\nwant    %+v", i, sfx[i], wantSFX[i])
		}
	}
	if len(plan.Automation) != 4 {
		t.Fatalf("automation = %+v, want 1 fade + 3 duck entries", plan.Automation)
	}
	fade := plan.Automation[0]
	if fade.TargetTrackID != "bgm" || fade.StartUS != 0 || fade.EndUS != 30_000_000 || fade.AttackUS != 500_000 || fade.ReleaseUS != 1_000_000 {
		t.Fatalf("fade automation = %+v", fade)
	}
	wantDuckWindows := [][2]int64{{0, 8_000_000}, {10_000_000, 18_000_000}, {20_000_000, 28_000_000}}
	for i, w := range wantDuckWindows {
		d := plan.Automation[i+1]
		if d.TargetTrackID != "bgm" || d.TriggerTrackID != "voiceover" || d.StartUS != w[0] || d.EndUS != w[1] || d.GainDB != -30 {
			t.Fatalf("duck automation[%d] = %+v, want window [%d,%d) at -30dB", i, d, w[0], w[1])
		}
	}
	if plan.DurationUS != 30_000_000 || plan.MixPolicy != audio.MixVoiceoverWithDuckedClip || plan.PlanSHA256 == "" {
		t.Fatalf("plan contract broken: duration=%d policy=%q sealed=%v", plan.DurationUS, plan.MixPolicy, plan.PlanSHA256 != "")
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("sealed gate C plan is invalid: %v", err)
	}

	// ── Real render + persistence for manual listening ──
	renderAssets := result.Assets
	for _, id := range []string{"vo-c1", "vo-c2", "vo-c3"} {
		renderAssets = append(renderAssets, audio.ResolvedAudioAsset{AssetID: id, Path: voPaths[id]})
	}
	master := renderGateAudio(t, musclesPath, ffmpegPath, plan, renderAssets, "C_full_mix")
	certifyGateMaster(t, ffprobePath, master.Path, 30_000_000)
	logGateTiming(t, "C_full_mix", compileMS, master)

	// ── Frequency certification of the master ──
	// Voiceover per scene: present while speaking, absent in the gaps.
	for _, w := range [][2]float64{{0, 8}, {10, 18}, {20, 28}} {
		if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, w[0], w[1], 800, 1200); got <= -30 {
			t.Fatalf("voiceover tone missing in [%g,%g): %.2f dB", w[0], w[1], got)
		}
	}
	for _, w := range [][2]float64{{8, 10}, {18, 20}, {28, 30}} {
		if got := bandMeanVolumeDB(t, ffmpegPath, master.Path, w[0], w[1], 800, 1200); got >= -25 {
			t.Fatalf("voiceover must be absent in [%g,%g): mean %.2f dB", w[0], w[1], got)
		}
	}
	// BGM loop restarts at 12s and 24s; the truncated tail covers to 30s.
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 11.5, 14, 30, 90); got <= -40 {
		t.Fatalf("bgm loop event 2 missing in [11.5,14): %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 23.5, 26, 30, 90); got <= -40 {
		t.Fatalf("bgm loop event 3 missing in [23.5,26): %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 27, 29.5, 30, 90); got <= -40 {
		t.Fatalf("bgm loop event 3 (truncated tail) missing in [27,29.5): %.2f dB", got)
	}
	// Ducking: during scene speech the music sits below its inter-scene
	// base level (measured in the [8,10) gap after the release ramp).
	ducked := bandMeanVolumeDB(t, ffmpegPath, master.Path, 1, 7, 30, 90)
	base := bandMeanVolumeDB(t, ffmpegPath, master.Path, 8.6, 9.8, 30, 90)
	if ducked >= base-4 {
		t.Fatalf("bgm ducking not effective: mean during VO %.2f dB vs base %.2f dB", ducked, base)
	}
	// The three effects land inside their ±0.6s windows in their own bands.
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 4.6, 6.1, 2700, 3300); got <= -15 {
		t.Fatalf("whoosh sfx missing at 5s: %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 14.6, 16.1, 4500, 5500); got <= -15 {
		t.Fatalf("impact sfx missing at 15s: %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 23.6, 25.1, 7500, 8500); got <= -20 {
		t.Fatalf("boom sfx missing at 24s: %.2f dB", got)
	}
	// And nothing smears outside the placements.
	if got := bandMeanVolumeDB(t, ffmpegPath, master.Path, 28.2, 29.5, 7500, 8500); got >= -45 {
		t.Fatalf("boom band must be silent before the video ends: mean %.2f dB", got)
	}
}

// TestListenGateSceneRelativeSFX30s: same timeline and BGM as gate C, but
// the effects are placed scene-relative: scene-c1 start+500ms → 500ms,
// scene-c2 middle → 15s, scene-c3 end−300ms → 29.7s (the 300ms source ends
// exactly on the video end).
func TestListenGateSceneRelativeSFX30s(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}
	ffprobePath := resolveFFprobeBinary(ffmpegPath)

	assetsDir := t.TempDir()
	bgmPath := filepath.Join(assetsDir, "bgm_r.wav") // 60Hz, 12s
	generateToneAsset(t, ffmpegPath, bgmPath, 60, 12)
	voPaths := make(map[string]string, 3)
	voDurations := make(map[string]int64, 3)
	for _, id := range []string{"vo-c1", "vo-c2", "vo-c3"} {
		path := filepath.Join(assetsDir, id+".wav") // 1000Hz, 8s each
		generateToneAsset(t, ffmpegPath, path, 1000, 8)
		voPaths[id] = path
		voDurations[id] = probeDurationUS(t, ffprobePath, path)
	}
	sfxSpecs := []struct {
		id        string
		freq      float64
		durationS float64
	}{
		{"sfx-r1", 3200, 0.5},
		{"sfx-r2", 5000, 0.5},
		{"sfx-r3", 6000, 0.3},
	}
	sfxPaths := make(map[string]string, len(sfxSpecs))
	sfxDurations := make(map[string]int64, len(sfxSpecs))
	for _, spec := range sfxSpecs {
		path := filepath.Join(assetsDir, spec.id+".wav")
		generateToneAsset(t, ffmpegPath, path, spec.freq, spec.durationS)
		sfxPaths[spec.id] = path
		sfxDurations[spec.id] = probeDurationUS(t, ffprobePath, path)
	}
	bgmDur := probeDurationUS(t, ffprobePath, bgmPath)

	timeline := gateCTimeline(voDurations)
	sfxIntents := []scriptpkg.SoundEffectIntent{
		{AssetID: "sfx-r1", SceneID: "scene-c1", Anchor: scriptpkg.SFXAnchorStart, OffsetMS: 500, GainDB: -8},
		{AssetID: "sfx-r2", SceneID: "scene-c2", Anchor: scriptpkg.SFXAnchorMiddle, GainDB: -6},
		{AssetID: "sfx-r3", SceneID: "scene-c3", Anchor: scriptpkg.SFXAnchorEnd, OffsetMS: -300, GainDB: -5},
	}
	source := mapAudioAssetSource{assets: map[string]audio.ResolvedAudioAsset{
		"bgm-c":  {AssetID: "bgm-c", Path: bgmPath, DurationUS: bgmDur},
		"sfx-r1": {AssetID: "sfx-r1", Path: sfxPaths["sfx-r1"], DurationUS: sfxDurations["sfx-r1"]},
		"sfx-r2": {AssetID: "sfx-r2", Path: sfxPaths["sfx-r2"], DurationUS: sfxDurations["sfx-r2"]},
		"sfx-r3": {AssetID: "sfx-r3", Path: sfxPaths["sfx-r3"], DurationUS: sfxDurations["sfx-r3"]},
	}}

	compileStarted := time.Now()
	result, err := scripts.CompileAudioWithIntents(context.Background(), timeline, audio.DefaultAudioProfile(), "voiceover_with_ducked_clip", gateCBGM(), sfxIntents, source)
	if err != nil {
		t.Fatalf("compile scene-relative audio intents: %v", err)
	}
	compileMS := time.Since(compileStarted).Milliseconds()
	plan := result.Plan

	// ── Plan gates: the anchors collapse to absolute microseconds ──
	sfx := trackEvents(plan, audio.TrackSFX)
	wantSFX := []audio.AudioEvent{
		{EventID: "sfx-0", Type: audio.EventSFX, AssetID: "sfx-r1", TimelineStartUS: 500_000, DurationUS: 500_000, SourceInUS: 0, SourceDurationUS: 500_000, GainDB: -8},
		{EventID: "sfx-1", Type: audio.EventSFX, AssetID: "sfx-r2", TimelineStartUS: 15_000_000, DurationUS: 500_000, SourceInUS: 0, SourceDurationUS: 500_000, GainDB: -6},
		{EventID: "sfx-2", Type: audio.EventSFX, AssetID: "sfx-r3", TimelineStartUS: 29_700_000, DurationUS: 300_000, SourceInUS: 0, SourceDurationUS: 300_000, GainDB: -5},
	}
	if len(sfx) != len(wantSFX) {
		t.Fatalf("sfx events = %+v, want %d scene-resolved placements", sfx, len(wantSFX))
	}
	for i := range wantSFX {
		if sfx[i] != wantSFX[i] {
			t.Fatalf("sfx[%d] = %+v\nwant    %+v", i, sfx[i], wantSFX[i])
		}
	}
	if trackEvents(plan, audio.TrackBGM) == nil || len(trackEvents(plan, audio.TrackBGM)) != 3 {
		t.Fatalf("bgm events = %+v, want 3 loop events", trackEvents(plan, audio.TrackBGM))
	}
	if plan.DurationUS != 30_000_000 || plan.PlanSHA256 == "" {
		t.Fatalf("plan contract broken: duration=%d sealed=%v", plan.DurationUS, plan.PlanSHA256 != "")
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("sealed scene-relative plan is invalid: %v", err)
	}

	// ── Real render + persistence for manual listening ──
	renderAssets := result.Assets
	for _, id := range []string{"vo-c1", "vo-c2", "vo-c3"} {
		renderAssets = append(renderAssets, audio.ResolvedAudioAsset{AssetID: id, Path: voPaths[id]})
	}
	master := renderGateAudio(t, musclesPath, ffmpegPath, plan, renderAssets, "C_scene_relative")
	certifyGateMaster(t, ffprobePath, master.Path, 30_000_000)
	logGateTiming(t, "C_scene_relative", compileMS, master)

	// ── Frequency certification: each anchor lands where resolved ──
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 0.4, 1.2, 2900, 3500); got <= -15 {
		t.Fatalf("scene-c1 start+500ms sfx missing at 0.5s: %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 14.6, 15.6, 4500, 5500); got <= -15 {
		t.Fatalf("scene-c2 middle sfx missing at 15s: %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 29.4, 30, 5500, 6500); got <= -20 {
		t.Fatalf("scene-c3 end-300ms sfx missing at 29.7s: %.2f dB", got)
	}
}
