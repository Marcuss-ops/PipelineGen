// Package rustexec — audio_gate_concurrent_render_test.go is the regression
// test for the parallel-component flake: the `rust` and `timeline` component
// suites execute the same rustexec test binary at the same time, and two
// renders of the SAME compiled plan used to write the same deterministic
// /tmp/pipelinegen-final-audio-<planSHA>.m4a. One process then probed (or
// hashed) a master the other was rewriting, and the listen gate failed with
// `ffprobe exited with status 1`.
package rustexec

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestConcurrentSamePlanRendersUseDistinctMasters renders one identical plan
// twice, concurrently, through two independent Rust workers — the in-process
// equivalent of `rust` and `timeline` running the same suite in parallel. Both
// masters must be complete and certifiable, which is only possible when each
// render owns its output path.
func TestConcurrentSamePlanRendersUseDistinctMasters(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles release binary not installed; run make rust-fetch")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}
	ffprobePath := resolveFFprobeBinary(ffmpegPath)
	if ffprobePath == "" {
		t.Skip("ffprobe binary not found")
	}

	voPath := t.TempDir() + "/vo_concurrent.wav"
	generateToneAsset(t, ffmpegPath, voPath, 1000, 6)
	voDur := probeDurationUS(t, ffprobePath, voPath)

	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 6_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene-concurrent", Index: 0, TimelineStartUS: 0, DurationUS: 6_000_000,
			AudioIntents: []audio.AudioIntent{{
				Mode:               audio.AudioVoiceover,
				VoiceoverAssetID:   "vo-concurrent",
				SourceDurationUS:   voDur,
				TimelineOffsetUS:   0,
				TimelineDurationUS: 6_000_000,
			}},
		}},
	}
	if err := timeline.Validate(); err != nil {
		t.Fatalf("concurrent canonical timeline is invalid: %v", err)
	}
	compiled, err := scripts.CompileAudioWithIntents(context.Background(), timeline, audio.DefaultAudioProfile(), "voiceover_with_ducked_clip", nil, nil, mapAudioAssetSource{})
	if err != nil {
		t.Fatalf("compile concurrent plan: %v", err)
	}
	plan := compiled.Plan
	assets := append(compiled.Assets, audio.ResolvedAudioAsset{AssetID: "vo-concurrent", Path: voPath})

	// Build both renderers before starting the goroutines so every
	// testing.T registration happens on the test goroutine.
	renderers := make([]*CombinedAudioRenderer, 2)
	for i := range renderers {
		executor := newRealExecutor(t, musclesPath, ffmpegPath)
		processor := &VideoProcessor{client: NewClientWithExecutor(executor, nil)}
		renderers[i], err = NewCombinedAudioRenderer(processor)
		if err != nil {
			t.Fatalf("combined audio renderer %d: %v", i, err)
		}
	}

	type renderResult struct {
		final scripts.FinalAudioReference
		err   error
	}
	results := make([]renderResult, len(renderers))
	var wg sync.WaitGroup
	for i, renderer := range renderers {
		i, renderer := i, renderer
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].final, _, results[i].err = renderer.Render(context.Background(), plan, assets)
		}()
	}
	wg.Wait()

	for i, result := range results {
		if result.err != nil {
			t.Fatalf("concurrent render %d failed: %v", i, result.err)
		}
	}
	if results[0].final.Path == results[1].final.Path {
		t.Fatalf("both renders wrote the same master %q", results[0].final.Path)
	}
	for i, result := range results {
		defer func(path string) { _ = os.Remove(path) }(result.final.Path)
		if got := probeDurationUS(t, ffprobePath, result.final.Path); got < 5_900_000 || got > 6_100_000 {
			t.Fatalf("render %d master duration = %dus, want ~6s", i, got)
		}
		if got := fileSHA256(t, result.final.Path); got != result.final.FinalAudioSHA256 {
			t.Fatalf("render %d master hash = %s, certified %s", i, got, result.final.FinalAudioSHA256)
		}
	}
}
