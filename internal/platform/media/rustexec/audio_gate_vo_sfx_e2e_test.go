// Package rustexec — audio_gate_vo_sfx_e2e_test.go is listen gate B:
// voiceover + three sound effects on a 20s timeline at unambiguous
// timestamps (3s / 8s / 15s) with explicit, audible gains. The narration
// occupies [0,2.5) so every effect lands in a speech-silent window and its
// timestamp can be certified without voiceover band leakage.
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

// TestListenGateVOPlusThreeSFX20s: short voiceover intro, then whoosh at
// 3s (-8dB), impact at 8s (-6dB) and boom at 15s (-5dB).
func TestListenGateVOPlusThreeSFX20s(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}
	ffprobePath := resolveFFprobeBinary(ffmpegPath)

	// Real assets: the effects sit ≥1.5 octaves above the 1000Hz
	// voiceover (3k/5k/8kHz) and play while the narration is silent, so
	// presence AND absence are measurable in their own bands.
	assetsDir := t.TempDir()
	voPath := filepath.Join(assetsDir, "vo_b.wav")
	generateToneAsset(t, ffmpegPath, voPath, 1000, 2.5)
	sfxSpecs := []struct {
		id        string
		freq      float64
		durationS float64
	}{
		{"sfx-b-whoosh", 3000, 2}, // >= explicit duration 900ms
		{"sfx-b-impact", 5000, 0.5},
		{"sfx-b-boom", 8000, 0.3},
	}
	sfxPaths := make(map[string]string, len(sfxSpecs))
	sfxDurations := make(map[string]int64, len(sfxSpecs))
	for _, spec := range sfxSpecs {
		path := filepath.Join(assetsDir, spec.id+".wav")
		generateToneAsset(t, ffmpegPath, path, spec.freq, spec.durationS)
		sfxPaths[spec.id] = path
		sfxDurations[spec.id] = probeDurationUS(t, ffprobePath, path)
	}
	voDur := probeDurationUS(t, ffprobePath, voPath)

	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 20_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene-b", Index: 0, TimelineStartUS: 0, DurationUS: 20_000_000,
			AudioIntents: []audio.AudioIntent{{
				Mode:               audio.AudioVoiceover,
				VoiceoverAssetID:   "vo-b",
				SourceDurationUS:   voDur,
				TimelineOffsetUS:   0,
				TimelineDurationUS: 20_000_000,
			}},
		}},
	}
	if err := timeline.Validate(); err != nil {
		t.Fatalf("gate B canonical timeline is invalid: %v", err)
	}

	sfxIntents := []scriptpkg.SoundEffectIntent{
		{AssetID: "sfx-b-whoosh", AtMS: 3_000, SourceInMS: 0, DurationMS: 900, GainDB: -8},
		{AssetID: "sfx-b-impact", AtMS: 8_000, GainDB: -6},
		{AssetID: "sfx-b-boom", AtMS: 15_000, GainDB: -5},
	}
	source := mapAudioAssetSource{assets: map[string]audio.ResolvedAudioAsset{
		"sfx-b-whoosh": {AssetID: "sfx-b-whoosh", Path: sfxPaths["sfx-b-whoosh"], DurationUS: sfxDurations["sfx-b-whoosh"]},
		"sfx-b-impact": {AssetID: "sfx-b-impact", Path: sfxPaths["sfx-b-impact"], DurationUS: sfxDurations["sfx-b-impact"]},
		"sfx-b-boom":   {AssetID: "sfx-b-boom", Path: sfxPaths["sfx-b-boom"], DurationUS: sfxDurations["sfx-b-boom"]},
	}}

	compileStarted := time.Now()
	result, err := scripts.CompileAudioWithIntents(context.Background(), timeline, audio.DefaultAudioProfile(), "voiceover_with_ducked_clip", nil, sfxIntents, source)
	if err != nil {
		t.Fatalf("compile gate B audio intents: %v", err)
	}
	compileMS := time.Since(compileStarted).Milliseconds()
	plan := result.Plan

	// ── Plan gates BEFORE listening ──
	vo := trackEvents(plan, audio.TrackVoiceover)
	if len(vo) != 1 {
		t.Fatalf("voiceover events = %d, want 1", len(vo))
	}
	bgm := trackEvents(plan, audio.TrackBGM)
	if len(bgm) != 0 {
		t.Fatalf("bgm events = %+v, want none in gate B", bgm)
	}
	sfx := trackEvents(plan, audio.TrackSFX)
	wantSFX := []audio.AudioEvent{
		{EventID: "sfx-0", Type: audio.EventSFX, AssetID: "sfx-b-whoosh", TimelineStartUS: 3_000_000, DurationUS: 900_000, SourceInUS: 0, SourceDurationUS: 900_000, GainDB: -8},
		{EventID: "sfx-1", Type: audio.EventSFX, AssetID: "sfx-b-impact", TimelineStartUS: 8_000_000, DurationUS: 500_000, SourceInUS: 0, SourceDurationUS: 500_000, GainDB: -6},
		{EventID: "sfx-2", Type: audio.EventSFX, AssetID: "sfx-b-boom", TimelineStartUS: 15_000_000, DurationUS: 300_000, SourceInUS: 0, SourceDurationUS: 300_000, GainDB: -5},
	}
	if len(sfx) != len(wantSFX) {
		t.Fatalf("sfx events = %+v, want %d", sfx, len(wantSFX))
	}
	for i := range wantSFX {
		if sfx[i] != wantSFX[i] {
			t.Fatalf("sfx[%d] = %+v\nwant    %+v", i, sfx[i], wantSFX[i])
		}
	}
	if len(plan.Automation) != 0 {
		t.Fatalf("automation = %+v, want none in gate B", plan.Automation)
	}
	if plan.DurationUS != 20_000_000 || plan.MixPolicy != audio.MixVoiceoverWithDuckedClip || plan.PlanSHA256 == "" {
		t.Fatalf("plan contract broken: duration=%d policy=%q sealed=%v", plan.DurationUS, plan.MixPolicy, plan.PlanSHA256 != "")
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("sealed gate B plan is invalid: %v", err)
	}

	// ── Real render + persistence for manual listening ──
	renderAssets := append(result.Assets, audio.ResolvedAudioAsset{AssetID: "vo-b", Path: voPath})
	master := renderGateAudio(t, musclesPath, ffmpegPath, plan, renderAssets, "B_vo_sfx")
	certifyGateMaster(t, ffprobePath, master.Path, 20_000_000)
	logGateTiming(t, "B_vo_sfx", compileMS, master)

	// ── Frequency certification of the master ──
	// Voiceover intro present, then gone.
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 0, 2.5, 800, 1200); got <= -30 {
		t.Fatalf("voiceover tone missing in [0,2.5): %.2f dB", got)
	}
	if got := bandMeanVolumeDB(t, ffmpegPath, master.Path, 4, 20, 800, 1200); got >= -25 {
		t.Fatalf("voiceover must be absent in [4,20): mean %.2f dB", got)
	}
	// Each effect present in its own band inside a ±0.5s window around
	// its placement…
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 2.6, 4.2, 2700, 3300); got <= -20 {
		t.Fatalf("whoosh sfx missing at 3s: %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 7.6, 9.2, 4500, 5500); got <= -20 {
		t.Fatalf("impact sfx missing at 8s: %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, master.Path, 14.6, 16.2, 7500, 8500); got <= -20 {
		t.Fatalf("boom sfx missing at 15s: %.2f dB", got)
	}
	// …and absent from the same bands before it fires — the placements
	// are point-in-time, not smeared across the timeline.
	if got := bandMeanVolumeDB(t, ffmpegPath, master.Path, 4, 7.5, 2700, 3300); got >= -45 {
		t.Fatalf("whoosh band must be silent before 3s: mean %.2f dB", got)
	}
	if got := bandMeanVolumeDB(t, ffmpegPath, master.Path, 10, 14.5, 4500, 5500); got >= -45 {
		t.Fatalf("impact band must be silent before 8..15s: mean %.2f dB", got)
	}
	if got := bandMeanVolumeDB(t, ffmpegPath, master.Path, 16.5, 19.5, 7500, 8500); got >= -45 {
		t.Fatalf("boom band must be silent after 15s: mean %.2f dB", got)
	}
}
