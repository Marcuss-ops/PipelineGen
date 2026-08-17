// Package scriptgeneration — runner_scene_text_ready_fanout_test.go certifies
// the canonical SceneTextReady fan-out: committing scene text starts
// SceneAnalysis (VidRush) per scene, and translation + TTS run as the other
// branches of the same fan-out — so TTS completes while analysis is still
// pending, never after the VidRush barrier.
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// voCallCount reads the stub voiceover generator's call counter under its
// mutex, so the test can observe TTS progress without a data race.
func voCallCount(v *stubVoiceoverGenerator) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.callCount
}

// TestSceneTextReady_TTSDoesNotWaitForAnalysis pins the fan-out contract: with
// SceneAnalysis blocked (enricher unreleased), the runner still completes
// translation + TTS. TTS therefore starts from the SceneTextReady boundary in
// parallel with analysis instead of waiting for the VidRush barrier.
func TestSceneTextReady_TTSDoesNotWaitForAnalysis(t *testing.T) {
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

	req := defaultTestRequest()
	runID := "run-fanout-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	// Wait until TTS has started. Analysis is still blocked (we have not
	// released the enricher), so a non-zero voiceover call count proves TTS
	// did not wait for the analysis branch.
	deadline := time.Now().Add(5 * time.Second)
	for voCallCount(voGen) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("TTS did not start while SceneAnalysis was still pending")
		}
		time.Sleep(time.Millisecond)
	}
	require.Greater(t, enricher.callCount(), 0, "SceneAnalysis must have started (and be blocked) when TTS starts")

	// Release analysis and let the run join + complete.
	close(enricher.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after releasing SceneAnalysis")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
	require.Equal(t, 3, enricher.callCount(), "each scene enriched exactly once")

	names := timeline.names()
	// Every scene analysis began during/after generation; the barrier (join)
	// completed last — after TTS and all enrichments.
	require.Contains(t, names, "vidrush scene-0 started")
	require.Contains(t, names, "vidrush scene-0 completed")
}
