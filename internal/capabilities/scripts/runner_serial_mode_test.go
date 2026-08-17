// Package scriptgeneration — runner_serial_mode_test.go certifies the
// serial-mode toggle used for controlled before/after benchmarking: when
// enabled, the VidRush/NLP branch completes blocking BEFORE TTS starts
// (entities → voiceover, no overlap), and both the NLP extraction and TTS
// pools are forced to concurrency 1. The default (disabled) is the parallel
// SceneTextReady DAG.
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSetSerialMode_ForcesTTSConcurrencyToOne(t *testing.T) {
	runner, _, _, _, _, _, _ := newTestRunner()
	require.Equal(t, DefaultTTSConcurrency, runner.ttsConcurrency)

	runner.SetSerialMode(true)
	require.True(t, runner.serialMode)
	require.Equal(t, 1, runner.ttsConcurrency, "serial mode must force a single-slot TTS pool")

	runner.SetSerialMode(false)
	require.False(t, runner.serialMode)
}

// TestSerialMode_EntitiesCompleteBeforeTTS pins the "before" ordering: with
// serial mode enabled and NLP blocked, TTS must NOT start (the run is waiting
// on the VidRush join before voiceover). After NLP is released, TTS runs and
// the run completes. This is the opposite of the parallel DAG, where TTS runs
// while NLP is still outstanding.
func TestSerialMode_EntitiesCompleteBeforeTTS(t *testing.T) {
	runner, repo, voGen, enricher := blockingVidRushRunner()
	runner.SetSerialMode(true)

	req := defaultTestRequest()
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	runID := "run-serial-mode"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	// NLP must have started (the enricher blocks), and TTS must still be
	// zero — serial mode waits for entities before voiceover.
	require.Eventually(t, func() bool { return enricher.callCount() >= 1 }, 2*time.Second, time.Millisecond,
		"NLP must start on the committed scenes")
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 0, voCallCount(voGen), "TTS must not start before NLP completes in serial mode")

	close(enricher.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after releasing NLP")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
	require.Equal(t, 3, voCallCount(voGen), "TTS must produce one voiceover per scene after entities complete")
}
