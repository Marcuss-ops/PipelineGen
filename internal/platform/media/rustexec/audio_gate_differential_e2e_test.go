// Package rustexec — audio_gate_differential_e2e_test.go renders the SAME
// voiceover four times — A: VO only, B: VO+BGM, C: VO+SFX, D: VO+BGM+SFX —
// and certifies that the four masters are genuinely different artifacts:
// pairwise-distinct SHA256, the BGM difference distributed along the whole
// file, and the SFX difference concentrated at its timestamps.
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

// gateDBGM loops a 10s source over the whole 20s timeline (exactly two
// events, no truncation) with an audible base gain ducked under the
// continuous narration.
func gateDBGM() []scriptpkg.BackgroundMusicIntent {
	return []scriptpkg.BackgroundMusicIntent{{
		AssetID:            "bgm-d",
		StartMS:            0,
		End:                &scriptpkg.AudioTimelineEnd{VideoEnd: true},
		Loop:               true,
		GainDB:             -18,
		DuckUnderVoiceover: true,
		DuckGainDB:         -28,
		DuckAttackMS:       120,
		DuckReleaseMS:      350,
	}}
}

// gateDSFX places the three effects at 3s / 8s / 15s with explicit gains.
func gateDSFX() []scriptpkg.SoundEffectIntent {
	return []scriptpkg.SoundEffectIntent{
		{AssetID: "sfx-d-whoosh", AtMS: 3_000, SourceInMS: 0, DurationMS: 900, GainDB: -8},
		{AssetID: "sfx-d-impact", AtMS: 8_000, GainDB: -6},
		{AssetID: "sfx-d-boom", AtMS: 15_000, GainDB: -5},
	}
}

// TestListenGateDifferentialABCD makes a faked PASS impossible: identical
// voiceover, four intent combinations, four different masters.
func TestListenGateDifferentialABCD(t *testing.T) {
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
	voPath := filepath.Join(assetsDir, "vo_d.wav") // 1000Hz, continuous 20s
	generateToneAsset(t, ffmpegPath, voPath, 1000, 20)
	bgmPath := filepath.Join(assetsDir, "bgm_d.wav") // 60Hz, 10s
	generateToneAsset(t, ffmpegPath, bgmPath, 60, 10)
	sfxSpecs := []struct {
		id        string
		freq      float64
		durationS float64
	}{
		{"sfx-d-whoosh", 3000, 2},
		{"sfx-d-impact", 5000, 0.5},
		{"sfx-d-boom", 8000, 0.3},
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
	bgmDur := probeDurationUS(t, ffprobePath, bgmPath)

	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 20_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene-d", Index: 0, TimelineStartUS: 0, DurationUS: 20_000_000,
			AudioIntents: []audio.AudioIntent{{
				Mode:               audio.AudioVoiceover,
				VoiceoverAssetID:   "vo-d",
				SourceDurationUS:   voDur,
				TimelineOffsetUS:   0,
				TimelineDurationUS: 20_000_000,
			}},
		}},
	}
	if err := timeline.Validate(); err != nil {
		t.Fatalf("differential canonical timeline is invalid: %v", err)
	}
	source := mapAudioAssetSource{assets: map[string]audio.ResolvedAudioAsset{
		"bgm-d":        {AssetID: "bgm-d", Path: bgmPath, DurationUS: bgmDur},
		"sfx-d-whoosh": {AssetID: "sfx-d-whoosh", Path: sfxPaths["sfx-d-whoosh"], DurationUS: sfxDurations["sfx-d-whoosh"]},
		"sfx-d-impact": {AssetID: "sfx-d-impact", Path: sfxPaths["sfx-d-impact"], DurationUS: sfxDurations["sfx-d-impact"]},
		"sfx-d-boom":   {AssetID: "sfx-d-boom", Path: sfxPaths["sfx-d-boom"], DurationUS: sfxDurations["sfx-d-boom"]},
	}}

	variants := []struct {
		name    string
		withBGM bool
		withSFX bool
	}{
		{name: "A_vo_only"},
		{name: "B_vo_bgm", withBGM: true},
		{name: "C_vo_sfx", withSFX: true},
		{name: "D_vo_bgm_sfx", withBGM: true, withSFX: true},
	}
	masters := make(map[string]gateRender, len(variants))
	for _, v := range variants {
		var bgmIntents []scriptpkg.BackgroundMusicIntent
		var sfxIntents []scriptpkg.SoundEffectIntent
		if v.withBGM {
			bgmIntents = gateDBGM()
		}
		if v.withSFX {
			sfxIntents = gateDSFX()
		}
		compileStarted := time.Now()
		result, err := scripts.CompileAudioWithIntents(context.Background(), timeline, audio.DefaultAudioProfile(), "voiceover_with_ducked_clip", bgmIntents, sfxIntents, source)
		if err != nil {
			t.Fatalf("compile variant %s: %v", v.name, err)
		}
		compileMS := time.Since(compileStarted).Milliseconds()
		plan := result.Plan

		// Plan gates before rendering.
		wantBGM, wantSFX, wantAutomation := 0, 0, 0
		switch {
		case v.withBGM && v.withSFX:
			wantBGM, wantSFX, wantAutomation = 2, 3, 1
		case v.withBGM:
			wantBGM, wantAutomation = 2, 1
		case v.withSFX:
			wantSFX = 3
		}
		if got := len(trackEvents(plan, audio.TrackVoiceover)); got != 1 {
			t.Fatalf("[%s] voiceover events = %d, want 1", v.name, got)
		}
		if got := len(trackEvents(plan, audio.TrackBGM)); got != wantBGM {
			t.Fatalf("[%s] bgm events = %d, want %d", v.name, got, wantBGM)
		}
		if got := len(trackEvents(plan, audio.TrackSFX)); got != wantSFX {
			t.Fatalf("[%s] sfx events = %d, want %d", v.name, got, wantSFX)
		}
		if got := len(plan.Automation); got != wantAutomation {
			t.Fatalf("[%s] automation entries = %d, want %d", v.name, got, wantAutomation)
		}
		if plan.DurationUS != 20_000_000 || plan.PlanSHA256 == "" {
			t.Fatalf("[%s] plan contract broken: duration=%d sealed=%v", v.name, plan.DurationUS, plan.PlanSHA256 != "")
		}

		renderAssets := append(result.Assets, audio.ResolvedAudioAsset{AssetID: "vo-d", Path: voPath})
		master := renderGateAudio(t, musclesPath, ffmpegPath, plan, renderAssets, "differential_"+v.name)
		certifyGateMaster(t, ffprobePath, master.Path, 20_000_000)
		logGateTiming(t, "differential_"+v.name, compileMS, master)
		masters[v.name] = master
	}

	// ── The four masters must be four different files ──
	names := []string{"A_vo_only", "B_vo_bgm", "C_vo_sfx", "D_vo_bgm_sfx"}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if masters[names[i]].SHA256 == masters[names[j]].SHA256 {
				t.Fatalf("%s and %s produced the identical master %s — the mix did nothing", names[i], names[j], masters[names[i]].SHA256)
			}
		}
	}
	a := masters["A_vo_only"].Path
	b := masters["B_vo_bgm"].Path
	c := masters["C_vo_sfx"].Path
	d := masters["D_vo_bgm_sfx"].Path

	// ── A vs B: the BGM difference is DISTRIBUTED along the file ──
	aBGMMean := bandMeanVolumeDB(t, ffmpegPath, a, 1, 19, 30, 90)
	if aBGMMean >= -35 {
		t.Fatalf("reference A already shows BGM-band energy: mean %.2f dB", aBGMMean)
	}
	for _, w := range [][2]float64{{1, 5}, {6, 10}, {11, 15}, {16, 19}} {
		if got := bandMaxVolumeDB(t, ffmpegPath, b, w[0], w[1], 30, 90); got <= -35 {
			t.Fatalf("B missing BGM energy in [%g,%g): %.2f dB", w[0], w[1], got)
		}
		if got := bandMaxVolumeDB(t, ffmpegPath, d, w[0], w[1], 30, 90); got <= -35 {
			t.Fatalf("D missing BGM energy in [%g,%g): %.2f dB", w[0], w[1], got)
		}
	}

	// ── A vs C: the SFX difference is CONCENTRATED at the timestamps ──
	sfxBands := []struct {
		low, high float64
		hit       [2]float64
		off       [2]float64
	}{
		{low: 2700, high: 3300, hit: [2]float64{2.7, 4.0}, off: [2]float64{5, 7.5}},
		{low: 4500, high: 5500, hit: [2]float64{7.7, 9.0}, off: [2]float64{10, 14.5}},
		{low: 7500, high: 8500, hit: [2]float64{14.7, 16.0}, off: [2]float64{16.5, 19.5}},
	}
	for _, band := range sfxBands {
		aHit := bandMaxVolumeDB(t, ffmpegPath, a, band.hit[0], band.hit[1], band.low, band.high)
		cHit := bandMaxVolumeDB(t, ffmpegPath, c, band.hit[0], band.hit[1], band.low, band.high)
		dHit := bandMaxVolumeDB(t, ffmpegPath, d, band.hit[0], band.hit[1], band.low, band.high)
		if cHit < aHit+6 {
			t.Fatalf("C does not rise above A inside the SFX window [%g,%g) of [%g,%g]Hz: C %.2f dB vs A %.2f dB", band.hit[0], band.hit[1], band.low, band.high, cHit, aHit)
		}
		if dHit < aHit+6 {
			t.Fatalf("D does not rise above A inside the SFX window [%g,%g) of [%g,%g]Hz: D %.2f dB vs A %.2f dB", band.hit[0], band.hit[1], band.low, band.high, dHit, aHit)
		}
		aOff := bandMeanVolumeDB(t, ffmpegPath, a, band.off[0], band.off[1], band.low, band.high)
		cOff := bandMeanVolumeDB(t, ffmpegPath, c, band.off[0], band.off[1], band.low, band.high)
		if delta := aOff - cOff; delta > 3 || delta < -3 {
			t.Fatalf("C diverges from A outside the SFX windows ([%g,%g) of [%g,%g]Hz): A %.2f dB vs C %.2f dB", band.off[0], band.off[1], band.low, band.high, aOff, cOff)
		}
	}
}
