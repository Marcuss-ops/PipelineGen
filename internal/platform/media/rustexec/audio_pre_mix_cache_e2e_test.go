// Package rustexec — audio_pre_mix_cache_e2e_test.go certifies the pre-mix
// stage reporting and the BGM loudness cache against the real installed
// renderer.
//
// The point of the test is the budget, not the audio quality (the listen gates
// own that): before prepare_ms/loudness_ms existed, `audio_render` could report
// 20+ s of wall on a short timeline with only the mix accounted for, and the
// whole-asset BGM loudness decode — by far the largest piece — had no owner.
// The two renders below are the same plan twice: the first must report a real
// loudness decode, the second must not pay for it again, and both must publish
// a byte-identical master.
package rustexec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestRenderAudioPlanReportsPreMixTimingsAndCachesLoudness(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles release binary not installed; run make rust-fetch")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}
	ffprobePath := resolveFFprobeBinary(ffmpegPath)

	cacheDir := t.TempDir()
	t.Setenv("PIPELINE_MUSCLES_CACHE_DIR", cacheDir)

	assetsDir := t.TempDir()
	bgmPath := filepath.Join(assetsDir, "bgm.wav")
	generateToneAsset(t, ffmpegPath, bgmPath, 60, 8)
	voPath := filepath.Join(assetsDir, "vo.wav")
	generateToneAsset(t, ffmpegPath, voPath, 1000, 8)
	voDur := probeDurationUS(t, ffprobePath, voPath)
	bgmDur := probeDurationUS(t, ffprobePath, bgmPath)

	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 8_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene-a", Index: 0, TimelineStartUS: 0, DurationUS: 8_000_000,
			AudioIntents: []audio.AudioIntent{{
				Mode:               audio.AudioVoiceover,
				VoiceoverAssetID:   "vo-a",
				SourceDurationUS:   voDur,
				TimelineOffsetUS:   0,
				TimelineDurationUS: 8_000_000,
			}},
		}},
	}
	if err := timeline.Validate(); err != nil {
		t.Fatalf("canonical timeline is invalid: %v", err)
	}

	bgmIntents := []scriptpkg.BackgroundMusicIntent{{
		AssetID:            "bgm-a",
		StartMS:            0,
		End:                &scriptpkg.AudioTimelineEnd{VideoEnd: true},
		Loop:               true,
		GainDB:             -18,
		DuckUnderVoiceover: true,
		DuckGainDB:         -24,
	}}
	source := mapAudioAssetSource{assets: map[string]audio.ResolvedAudioAsset{
		"bgm-a": {AssetID: "bgm-a", Path: bgmPath, DurationUS: bgmDur},
	}}
	result, err := scripts.CompileAudioWithIntents(
		context.Background(), timeline, audio.DefaultAudioProfile(),
		"voiceover_with_ducked_clip", bgmIntents, nil, source,
	)
	if err != nil {
		t.Fatalf("compile audio intents: %v", err)
	}
	plan := result.Plan
	renderAssets := append(result.Assets, audio.ResolvedAudioAsset{AssetID: "vo-a", Path: voPath})

	cold := renderGateAudio(t, musclesPath, ffmpegPath, plan, renderAssets, "pre_mix_cold")
	if cold.Metrics.PrepareMS < 0 {
		t.Fatalf("prepare_ms must be a measured wall, got %d", cold.Metrics.PrepareMS)
	}
	if cold.Metrics.LoudnessMS <= 0 {
		t.Fatalf(
			"a cold render must report the BGM loudness decode it paid for, got %+v",
			cold.Metrics,
		)
	}
	if cold.Metrics.MixMS <= 0 || cold.Metrics.AACEncodeMS <= 0 {
		t.Fatalf("the stage budget must stay complete, got %+v", cold.Metrics)
	}

	// Same plan, same asset file, same cache directory: the loudness stage has
	// nothing left to decode.
	warm := renderGateAudio(t, musclesPath, ffmpegPath, plan, renderAssets, "pre_mix_warm")
	if warm.Metrics.LoudnessMS != 0 {
		t.Fatalf(
			"a cached BGM measurement must not be reported as decode time, got %d ms",
			warm.Metrics.LoudnessMS,
		)
	}
	if warm.Metrics.MixMS <= 0 {
		t.Fatalf("the warm render must still measure the mix, got %+v", warm.Metrics)
	}
	if warm.SHA256 != cold.SHA256 {
		t.Fatalf(
			"the cached trim changed the master: cold %s, warm %s",
			cold.SHA256, warm.SHA256,
		)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("read loudness cache dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the cold render must have written a loudness cache entry")
	}
}
