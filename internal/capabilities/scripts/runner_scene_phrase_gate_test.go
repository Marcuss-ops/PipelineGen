// Package scriptgeneration — runner_scene_phrase_gate_test.go pins the gate
// that turns OFF the generate↔downstream overlap for one specific request
// shape, and records the measured cost of that decision.
//
// Measured (2026-09-28, crime-case shape, RTX A4000 host): the run's critical
// path is generate(21s) → scene_analysis(46s) strictly sequential because the
// payload carries `media_plan.extraction.important_phrases`. With streaming the
// provider fan-out (artlist resolve) for scene N starts while segment N+1 is
// still generating, so up to ~(generation wall − first-segment wall) of the
// artlist work would be hidden.
//
// Why the gate is CORRECT as written: ensureRequestedImportantPhrases appends
// caller-owned hints to the generated narration AFTER generation and only when
// no generated scene already contains them. Streaming consumers (NLP, TTS,
// render) observe each scene's text the moment it is committed, so streaming a
// hint-less prefix produced a narration/TTS-timeline mismatch (observed
// historically: 116 script tokens vs 95 TTS word boundaries). Turning the gate
// off is a CONTENT-contract change (per-scene hint resolution), not a
// refactor — this test keeps the current contract from drifting silently while
// the trade-off and its measured cost are recorded in
// refactored/docs/PIPELINE-WASTE-AUDIT-2026-09-12.md §19 ("Status update —
// 2026-09-28: gate Drive fair e la coda di audio_publish").
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

// TestSceneTextStreaming_ImportantPhraseHintsForceBatchPath pins the gate:
// explicit segment hints turn a streamable, explicitly-segmented request into a
// batch one. The proof is that the streaming API is never entered
// (gatedStreamingTextGenerator closes `emitted` only inside
// GenerateSceneTextStream) while the run still completes.
func TestSceneTextStreaming_ImportantPhraseHintsForceBatchPath(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	streamer := newGatedStreamingTextGenerator(defaultTestScenes())
	runner.textGen = streamer
	observer := &recordingSceneCommitObserver{}
	runner.SetSceneCommitObserver(observer)

	req := defaultTestRequest()
	// Explicit segments alone are streamable (see
	// TestSceneTextStreaming_ExplicitSegmentsUseProductionPath)...
	req.ScriptParams.Segments = []scriptpkg.ScriptSegment{
		{ID: "scene-0", Topic: "first"},
		{ID: "scene-1", Topic: "second"},
		{ID: "scene-2", Topic: "third"},
	}
	req.ScriptParams.SingleScene = false
	// ...but ONE editorial phrase hint is enough to force the barrier.
	req.MediaPlan.Extraction.ImportantPhrases = []string{"A investigação segue aberta."}

	runID := "run-stream-phrase-gate-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() { defer close(done); runner.Execute(context.Background(), runID, req) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete")
	}

	final := awaitCompletion(t, repo, runID, time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)

	select {
	case <-streamer.emitted:
		t.Fatal("the streaming API was entered for a request carrying important-phrase hints")
	default:
	}

	// The batch path still commits every scene exactly once, in order: the gate
	// changes WHEN the fan-out starts, never the scene topology it observes.
	final2 := observer.committed()
	require.Len(t, final2, 3)
	for i, event := range final2 {
		require.Equal(t, i, event.SceneIndex)
	}
}
