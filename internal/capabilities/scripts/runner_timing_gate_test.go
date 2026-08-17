// Package scriptgeneration — runner_timing_gate_test.go pins two ordering
// guarantees that the SceneTextReady DAG must never break:
//
//  1. CanonicalTimeline is gated by certified TTS timing: the timeline is not
//     compiled until the voiceover phase completes, and each segment's
//     duration agrees with the certified word-level SpeechTimingArtifact.
//  2. overlay.render waits only for the frozen (timing-certified) OverlayPlan,
//     never for overlay.prepare: render enqueues the sealed plan even when no
//     prepare enqueuer is wired.
package scriptgeneration

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// blockingTimingVoiceoverGenerator blocks every TTS call until released, then
// returns a voiceover whose AudioReference carries the canonical word-level
// SpeechTimingArtifact (100ms per word, same synthesis stream as the audio).
// Blocking lets the test prove CanonicalTimeline is not compiled while TTS is
// still pending; the timing artifact lets it assert the compiled timeline is
// anchored to the certified word boundaries.
type blockingTimingVoiceoverGenerator struct {
	release chan struct{}
	started atomic.Int32
}

func (g *blockingTimingVoiceoverGenerator) Generate(ctx context.Context, input VoiceoverInput) (AudioReference, error) {
	g.started.Add(1)
	select {
	case <-g.release:
	case <-ctx.Done():
		return AudioReference{}, ctx.Err()
	}
	words := strings.Fields(input.Text)
	boundaries := make([]capabilityaudio.SpeechWordTiming, len(words))
	for i, w := range words {
		boundaries[i] = capabilityaudio.SpeechWordTiming{
			Index:   i,
			Text:    w,
			StartUS: int64(i) * 100_000,
			EndUS:   int64(i+1) * 100_000,
		}
	}
	return AudioReference{
		ID:       "vo-" + input.SceneID + "-" + string(input.Language),
		FilePath: "/tmp/voiceover-" + input.SceneID + "-" + string(input.Language) + ".mp3",
		Duration: float64(len(words)) * 0.1,
		Timing: &capabilityaudio.SpeechTimingArtifact{
			Version:      capabilityaudio.SpeechTimingVersion,
			Provider:     "edge_tts",
			BoundaryMode: capabilityaudio.BoundaryWord,
			Language:     string(input.Language),
			TextSHA256:   "text-hash",
			AudioSHA256:  "audio-hash",
			DurationUS:   int64(len(words)) * 100_000,
			Words:        boundaries,
		},
	}, nil
}

// TestCanonicalTimeline_WaitsForTTS pins the first guarantee: while TTS is
// blocked the run must not compile a CanonicalTimeline, and once TTS completes
// each timeline segment's duration equals the scene's certified word-timing
// duration (never a text-length estimate).
func TestCanonicalTimeline_WaitsForTTS(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	voiceoverGen := &blockingTimingVoiceoverGenerator{release: make(chan struct{})}
	docPub := newStubDocumentPublisher()

	runner := NewRunner(repo, textGen, translator, voiceoverGen, docPub, canonicalTestDocumentRenderer{})
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	runID := "run-canonical-waits-tts"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	// Wait until TTS has started, then prove the timeline has NOT been
	// compiled while TTS is still blocked.
	deadline := time.Now().Add(5 * time.Second)
	for voiceoverGen.started.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("TTS did not start")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	run, err := repo.Get(context.Background(), runID)
	require.NoError(t, err)
	if run.Result != nil && run.Result.CanonicalTimeline != nil {
		t.Fatal("CanonicalTimeline must not be compiled while TTS is still pending")
	}

	// Release TTS; the run must complete and the timeline must be anchored to
	// the certified word timing.
	close(voiceoverGen.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after TTS released")
	}
	final := awaitCompletion(t, repo, runID, time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "run must complete: %s", final.ErrorMessage)

	res := final.Result
	require.NotNil(t, res, "result must be present")
	require.NotNil(t, res.CanonicalTimeline, "canonical timeline must be persisted")
	require.Len(t, res.CanonicalTimeline.Segments, 3)
	for i, scene := range res.Scenes {
		ref, ok := scene.Voiceover["en"]
		require.True(t, ok, "scene %d must carry an en voiceover", i)
		require.NotNil(t, ref.Timing, "scene %d must carry certified word timing", i)
		require.NoError(t, ref.Timing.Validate())
		require.Equal(t, ref.Timing.DurationUS, res.CanonicalTimeline.Segments[i].DurationUS,
			"scene %d timeline duration must equal the certified word-timing duration", i)
	}
}

// recordingOverlayRenderEnqueuer captures the frozen OverlayPlan the runner
// submits to overlay.render.
type recordingOverlayRenderEnqueuer struct {
	mu   sync.Mutex
	plan *capabilityoverlay.OverlayPlan
}

func (e *recordingOverlayRenderEnqueuer) EnqueueChrononPlan(_ context.Context, plan capabilityoverlay.OverlayPlan) (RenderReference, error) {
	e.mu.Lock()
	cp := plan
	e.plan = &cp
	e.mu.Unlock()
	return RenderReference{JobID: plan.PlanID, Status: "COMPLETED"}, nil
}

func (e *recordingOverlayRenderEnqueuer) captured() *capabilityoverlay.OverlayPlan {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.plan == nil {
		return nil
	}
	cp := *e.plan
	return &cp
}

// TestOverlayRender_WaitsForFrozenTimingNotPrepare pins the second guarantee:
// overlay.render is enqueued with the sealed, timing-frozen OverlayPlan (real
// microsecond offsets, never PENDING estimates) and does NOT require an
// overlay.prepare enqueuer — render waits only for the frozen canonical
// timing, not for prepare to finish.
func TestOverlayRender_WaitsForFrozenTimingNotPrepare(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator([]Scene{
		{
			ID: "scene-0", Index: 0,
			Text:        map[Language]string{"en": "Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units."},
			Annotations: overlayScene0Annotations(),
			Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		},
	})
	docPub := newStubDocumentPublisher()
	renderEnq := &recordingOverlayRenderEnqueuer{}
	runner := NewRunner(repo, textGen, newStubTranslator(), &entityTimelineVoiceoverGenerator{}, docPub, canonicalTestDocumentRenderer{})
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})
	runner.SetOverlayRegistry(capabilityoverlay.DefaultChrononOverlayRegistry)
	// Deliberately NO overlay.prepare enqueuer: render must not wait for it.
	runner.SetOverlayRenderEnqueuer(renderEnq)

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Project = "overlay-render-frozen"

	runID := "run-overlay-render-frozen"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "run must complete: %s", final.ErrorMessage)

	// Render was enqueued with the frozen plan even though prepare was never
	// wired — proving render does not depend on prepare.
	plan := renderEnq.captured()
	require.NotNil(t, plan, "overlay.render must be enqueued with the frozen plan")
	require.NoError(t, plan.Validate())
	require.Equal(t, runID, plan.PlanID)
	require.NotEmpty(t, plan.Items, "frozen plan must carry overlay items")

	// The plan items are anchored to the certified frozen timing (real
	// microsecond offsets), never a PENDING/estimate placeholder.
	timed := 0
	for _, item := range plan.Items {
		if item.DurationUS > 0 {
			timed++
		}
	}
	require.Greater(t, timed, 0, "frozen plan must carry certified microsecond timing")

	// The render reference is persisted on the durable result.
	require.NotNil(t, final.Result.OverlayRender, "render reference must be persisted")
	require.Equal(t, runID, final.Result.OverlayRender.JobID)
}
