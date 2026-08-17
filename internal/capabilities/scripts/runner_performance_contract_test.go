// Package scriptgeneration — runner_performance_contract_test.go is the
// canonical contract-test surface for the script → NLP → TTS → docs
// performance DAG. Each test pins one ordering/concurrency guarantee from the
// SceneTextReady fan-out so a future refactor that re-serializes the branches
// (or confuses accumulated parallel work with wall time) fails here.
//
// TestCanonicalTimeline_WaitsForTTS already lives in runner_timing_gate_test.go
// and is intentionally NOT duplicated here.
package scriptgeneration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// ── Shared concurrency-tracking doubles ────────────────────────────────

// concurrencyTrackingEnricher records the maximum number of simultaneously
// in-flight enrichment calls so a test can prove the NLP extraction pool is
// bounded at the certified concurrency (never unbounded fan-out).
type concurrencyTrackingEnricher struct {
	mu       sync.Mutex
	inflight int
	max      int
}

func (e *concurrencyTrackingEnricher) Enrich(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	e.mu.Lock()
	e.inflight++
	if e.inflight > e.max {
		e.max = e.inflight
	}
	e.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	e.mu.Lock()
	e.inflight--
	e.mu.Unlock()
	return scriptpkg.VidRushSegmentResult{
		SegmentID: scene.ID,
		SceneID:   scene.ID,
		Position:  scene.Index,
		Text:      scene.Text,
		TextHash:  SceneTextHash(scene.Text),
	}, nil
}

func (e *concurrencyTrackingEnricher) maxConcurrent() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.max
}

// concurrencyTrackingVoiceoverGenerator records the maximum number of
// simultaneously in-flight TTS calls so a test can prove the TTS pool is
// bounded at the certified concurrency.
type concurrencyTrackingVoiceoverGenerator struct {
	mu       sync.Mutex
	inflight int
	max      int
}

func (g *concurrencyTrackingVoiceoverGenerator) Generate(_ context.Context, input VoiceoverInput) (AudioReference, error) {
	g.mu.Lock()
	g.inflight++
	if g.inflight > g.max {
		g.max = g.inflight
	}
	g.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	g.mu.Lock()
	g.inflight--
	g.mu.Unlock()
	return AudioReference{
		ID:       "vo-" + input.SceneID + "-" + string(input.Language),
		FilePath: "/tmp/voiceover-" + input.SceneID + "-" + string(input.Language) + ".mp3",
		Duration: 1.0,
	}, nil
}

func (g *concurrencyTrackingVoiceoverGenerator) maxConcurrent() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.max
}

// blockingVidRushRunner wires a runner with a blocking NLP enricher so tests
// can hold the SceneAnalysis branch open while observing the TTS branch.
func blockingVidRushRunner() (*Runner, *inMemRunRepository, *stubVoiceoverGenerator, *e2eBlockingEnricher) {
	runner, repo, _, _, voGen, _, _ := newTestRunner()
	timeline := &timelineRecorder{}
	enricher := newE2EBlockingEnricher(timeline)
	runner.SetVidRushPipeline(&VidRushPipeline{
		Enricher: enricher,
		PlanResolver: VidRushPlanResolverFunc(func(_ context.Context, _ GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
			return &scriptpkg.ResolvedGenerationPlan{Language: "en", Title: "test"}, nil
		}),
		Backpressure: DefaultVidRushBackpressure(),
	})
	return runner, repo, voGen, enricher
}

// TestSceneTextReady_FansOutNLPAndTTS pins the fan-out contract: committing
// scene text starts BOTH the NLP/entity branch (VidRush enrichment) and the
// TTS branch. With NLP blocked, both branches must have started — never just
// one of them.
func TestSceneTextReady_FansOutNLPAndTTS(t *testing.T) {
	runner, repo, voGen, enricher := blockingVidRushRunner()
	req := defaultTestRequest()
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	runID := "run-fanout-nlp-tts"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	// Both branches must start while NLP is still blocked.
	deadline := time.Now().Add(5 * time.Second)
	for voCallCount(voGen) == 0 || enricher.callCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("fan-out incomplete: tts=%d nlp=%d (both must start)", voCallCount(voGen), enricher.callCount())
		}
		time.Sleep(time.Millisecond)
	}

	close(enricher.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after releasing NLP")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
}

// TestNLPAndTTS_StartWithoutWaitingForEachOther pins the independence of the
// two branches: TTS completes all scene voiceovers while NLP is still blocked,
// so TTS never waits for NLP to finish. (The reverse — NLP not waiting for TTS
// — is pinned by TestOverlayPrepare_StartsBeforeTTSCompletes.)
func TestNLPAndTTS_StartWithoutWaitingForEachOther(t *testing.T) {
	runner, repo, voGen, enricher := blockingVidRushRunner()
	req := defaultTestRequest()
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	runID := "run-nlp-tts-independent"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	// All 3 scene voiceovers must complete while NLP is still blocked.
	require.Eventually(t, func() bool { return voCallCount(voGen) == 3 }, 5*time.Second, time.Millisecond,
		"TTS must complete all scene voiceovers without waiting for NLP")

	// The run must still be blocked on the NLP join (proving the assertion
	// above happened while NLP was genuinely outstanding).
	select {
	case <-done:
		t.Fatal("run completed while NLP was still blocked")
	case <-time.After(50 * time.Millisecond):
	}

	close(enricher.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after releasing NLP")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
}

// TestOverlayPrepare_StartsBeforeTTSCompletes pins the prepare-branch contract:
// as soon as NLP results are available the runner plans OverlayIntents and
// enqueues overlay.prepare, while TTS is still synthesizing — prepare never
// waits for TTS or final audio.
func TestOverlayPrepare_StartsBeforeTTSCompletes(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator([]Scene{{
		ID:          "scene-0",
		Index:       0,
		Text:        map[Language]string{"en": "Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units."},
		Annotations: overlayScene0Annotations(),
		Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
	}})
	docPub := newStubDocumentPublisher()
	prepEnq := &countingPrepareEnqueuer{}
	blockingVO := &blockingTimingVoiceoverGenerator{release: make(chan struct{})}

	runner := NewRunner(repo, textGen, newStubTranslator(), blockingVO, docPub, canonicalTestDocumentRenderer{})
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})
	runner.SetOverlayRegistry(capabilityoverlay.DefaultChrononOverlayRegistry)
	runner.SetOverlayPrepareEnqueuer(prepEnq)

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Project = "overlay-prepare-before-tts"

	runID := "run-overlay-prepare-before-tts"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for blockingVO.started.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("TTS did not start")
		}
		time.Sleep(time.Millisecond)
	}
	require.Eventually(t, func() bool { return prepEnq.count() == 1 }, 2*time.Second, 5*time.Millisecond,
		"overlay.prepare must be enqueued while TTS is still blocked")

	close(blockingVO.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after TTS released")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
}

// TestDocsPrepare_DoesNotBlockTTS pins the docs-phase placement: document
// preparation (document.prepare) starts only after the voiceover stage has
// finished, so docs work can never block TTS (or NLP or audio).
func TestDocsPrepare_DoesNotBlockTTS(t *testing.T) {
	runner, repo, _, _, voGen, _, _ := newTestRunner()

	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-docs-noblock", AttemptID: "attempt-1"})
	runCtx := kernobs.WithRun(context.Background(), run)

	req := defaultTestRequest()
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	runID := "run-docs-does-not-block-tts"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(runCtx, runID, req)
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, 5*time.Second).Status)
	run.Finish()

	// Every scene has its voiceover: TTS fully completed.
	require.Equal(t, len(defaultTestScenes()), voCallCount(voGen), "TTS must produce one voiceover per scene")

	var voiceoverFinished, docPrepareStarted time.Time
	for _, st := range run.Report().Stages {
		switch st.Name {
		case voiceoverStage:
			voiceoverFinished = st.FinishedAt
		case string(StageDocumentPrepare):
			docPrepareStarted = st.StartedAt
		}
	}
	require.False(t, voiceoverFinished.IsZero(), "voiceover stage must be recorded")
	require.False(t, docPrepareStarted.IsZero(), "document.prepare stage must be recorded")
	require.False(t, voiceoverFinished.After(docPrepareStarted),
		"document.prepare must start after voiceover finishes, never before")
}

// TestParallelFanout_WallTimeIsNotAccumulatedWork pins the fan-out metric
// contract: parallel per-call work is reported as accumulated work_ms and
// max_ms, NEVER folded into wall_ms. One stage wrapping N parallel calls has
// wall_ms ≈ the longest call, while work_ms ≈ N × the per-call duration.
func TestParallelFanout_WallTimeIsNotAccumulatedWork(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-fanout", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	const perCall = 40 * time.Millisecond
	_, err := kernobs.MeasureStageReport(ctx, StageSceneAnalysis, func(stageCtx context.Context) error {
		var wg sync.WaitGroup
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = kernobs.MeasureOperation(stageCtx, kernobs.OperationInfo{
					Stage:     StageSceneAnalysis,
					Component: kernobs.ComponentNLP,
					Operation: kernobs.OperationExtract,
				}, func(opCtx context.Context) error {
					time.Sleep(perCall)
					return nil
				})
			}()
		}
		wg.Wait()
		return nil
	})
	require.NoError(t, err)
	run.Finish()

	var fanout *kernobs.FanoutReport
	for _, f := range run.Report().FanoutReports() {
		if f.Stage == string(StageSceneAnalysis) {
			fanout = &f
			break
		}
	}
	require.NotNil(t, fanout, "scene_analysis fan-out report must be present")
	require.Equal(t, int64(3), fanout.Calls, "three parallel calls must be counted")
	require.GreaterOrEqual(t, fanout.WorkMs, int64(3*perCall/time.Millisecond),
		"work_ms must accumulate all parallel calls, got %d", fanout.WorkMs)
	require.Less(t, fanout.MaxMs, fanout.WorkMs,
		"max_ms (single longest call) must be less than the accumulated work")
	require.Less(t, fanout.WallMs, fanout.WorkMs,
		"wall_ms must NOT equal accumulated parallel work, got wall=%d work=%d", fanout.WallMs, fanout.WorkMs)
}

// TestConcurrencyBound_NLP pins the certified NLP bound: committing more
// scenes than the pool capacity must never run more than DefaultNLPConcurrency
// enrichments concurrently (unbounded fan-out would trip rate limits/CPU).
func TestConcurrencyBound_NLP(t *testing.T) {
	enricher := &concurrencyTrackingEnricher{}
	coordinator := NewVidRushIncrementalCoordinatorWithBackpressure(enricher, nil, DefaultVidRushBackpressure())

	for i := 0; i < 10; i++ {
		commit(t, coordinator, "run-nlp-bound", fmt.Sprintf("scene-%d", i), i, fmt.Sprintf("Scene %d narration text", i), 1)
	}
	results, err := coordinator.WaitForVidRush(context.Background(), "run-nlp-bound")
	require.NoError(t, err)
	require.Len(t, results, 10)
	require.LessOrEqual(t, enricher.maxConcurrent(), DefaultNLPConcurrency,
		"NLP extraction must never exceed the certified concurrency bound")
}

// TestConcurrencyBound_TTS pins the certified TTS bound: a scene×language
// fan-out larger than the pool capacity must never run more than
// DefaultTTSConcurrency synthesis calls concurrently.
func TestConcurrencyBound_TTS(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	tracker := &concurrencyTrackingVoiceoverGenerator{}
	runner.voiceoverGen = tracker

	req := defaultTestRequest()
	runID := "run-tts-bound"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, 5*time.Second).Status)
	require.LessOrEqual(t, tracker.maxConcurrent(), DefaultTTSConcurrency,
		"TTS must never exceed the certified concurrency bound")
}

// TestPipeline_NoArtificialBarrierBetweenScriptAndNLPVoiceover pins the
// end-to-end chain: scene 0's NLP enrichment starts while the scene-text
// generator is still emitting later scenes (no barrier between script and
// NLP), and TTS starts while NLP is still outstanding (no barrier between NLP
// and voiceover).
func TestPipeline_NoArtificialBarrierBetweenScriptAndNLPVoiceover(t *testing.T) {
	runner, repo, voGen, enricher := blockingVidRushRunner()
	streamer := newGatedStreamingTextGenerator(defaultTestScenes())
	runner.textGen = streamer

	req := defaultTestRequest()
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	runID := "run-no-artificial-barrier"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	// Scene 0 is emitted, but the streamer is blocked before scene 1: the
	// whole script does NOT exist yet. NLP must already be enriching scene 0.
	select {
	case <-streamer.emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("streamer did not emit scene 0")
	}
	require.Eventually(t, func() bool { return enricher.callCount() >= 1 }, 2*time.Second, time.Millisecond,
		"NLP must enrich scene 0 before the full script is generated")

	// Release the streamer (script completes), then TTS must start while NLP
	// is still blocked — voiceover does not wait for the NLP join.
	close(streamer.release)
	require.Eventually(t, func() bool { return voCallCount(voGen) > 0 }, 5*time.Second, time.Millisecond,
		"TTS must start while NLP is still outstanding")

	close(enricher.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after releasing NLP")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
}
