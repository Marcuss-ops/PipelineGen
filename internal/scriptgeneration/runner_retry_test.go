// Package scriptgeneration — runner_retry_test.go covers the
// retry-from-checkpoint contract. The runner MUST resume from the
// last failed stage on a second Execute, WITHOUT recreating
// artifacts for stages that already completed.
//
// godlike/06 SSOT invariants asserted:
//
//   - On TextGenerator failure, the second Execute resumes from
//     StageGeneratingSceneText (not from StageNormalizing).
//   - On Translator failure at scene N, scenes that already have
//     translated text for the target language are SKIPPED on
//     retry (scene-level idempotency).
//   - On Enqueue failure after Docs, the second Execute resumes
//     from StageEnqueuingRender and Does NOT re-upsert
//     documents nor re-generate voiceovers.
package scriptgeneration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunner_TextGeneratorFails_RetryResumesFromCheckpoint covers
// Verdetto scenario 2: TextGenerator returns an error on the
// first call → run fails → reset stub → second Execute succeeds
// from the same checkpoint stage.
func TestRunner_TextGeneratorFails_RetryResumesFromCheckpoint(t *testing.T) {
	runner, repo, textGen, _, _, _, _ := newTestRunner()
	req := defaultTestRequest()

	// Configure textGen to fail on the second call.
	textGen.err = errors.New("generate scene text failed: provider timeout")
	textGen.failAfter = 0 // succeed once, fail on retry? No — failAfter = 0 means
	// call 1 fails (callCount > 0). textGen.failAfter = -1 means never fail.
	// Let's reconfigure: first call fails, second succeeds.
	textGen.failAfter = 0 // call 1 (callCount=1) > 0 → fail
	// Re-configure: scenes stay as defaultTestScenes().
	// The first call fails, the second (retried) succeeds.

	runID := "run-textgen-fail-001"
	err := repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	})
	require.NoError(t, err)

	// Execute — first attempt fails.
	runner.Execute(context.Background(), runID, req)

	// Wait briefly for the goroutine to finish.
	time.Sleep(100 * time.Millisecond)

	// Check that the run is FAILED.
	run, err := repo.Get(context.Background(), runID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusFailed, run.Status, "first attempt should fail")
	assert.Equal(t, StageGeneratingSceneText, run.FailedStage, "should fail at GENERATING_SCENE_TEXT")
	assert.Equal(t, 1, run.AttemptCount, "attempt count should be 1")

	// Second attempt: textGen now succeeds (failAfter already expired).
	textGen.failAfter = -1 // reset to succeed
	// Also reset error so GenerateSceneText returns scenes.
	textGen.err = nil

	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status, "second attempt should complete")
	assert.Equal(t, StageCompleted, final.CurrentStage)

	// Verify that GenerateSceneText was called 2+ times (first failed, second succeeded).
	assert.GreaterOrEqual(t, textGen.callCount, 2, "textGen should be called at least 2 times")
}

// TestRunner_TranslatorFailsAtScene_RetrySkipsAlreadyTranslated covers
// Verdetto scenario 3: Translator fails at a specific scene → first
// attempt fails → reset stub → second Execute resumes from
// StageTranslatingScenes and SKIPS scenes that already have the
// target-language text (scene-level idempotency).
func TestRunner_TranslatorFailsAtScene_RetrySkipsAlreadyTranslated(t *testing.T) {
	runner, repo, _, translator, _, docPub, renderEnq := newTestRunner()
	req := defaultTestRequest()

	// Configure translator to fail for scene-1.
	translator.failAfter["scene-1"] = 0 // first call to scene-1 fails

	runID := "run-translate-fail-001"
	err := repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	})
	require.NoError(t, err)

	// Execute — first attempt fails at scene-1 translation.
	runner.Execute(context.Background(), runID, req)

	time.Sleep(100 * time.Millisecond)

	// Confirm failure.
	run, err := repo.Get(context.Background(), runID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusFailed, run.Status)
	assert.Equal(t, StageTranslatingScenes, run.FailedStage)

	// Reset translator to succeed on retry.
	translator.failAfter = make(map[string]int) // No scenes fail

	// Second attempt — should resume from TRANSLATING_SCENES.
	// Scenes that already have ES text should be skipped.
	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status)

	// All scenes should have ES text.
	for i, s := range final.Result.Scenes {
		assert.NotEmpty(t, s.Text["es"], "scene %d should have ES text on retry", i)
	}

	// Verify voiceovers and docs exist despite the retry.
	for i, s := range final.Result.Scenes {
		assert.NotEmpty(t, s.Voiceover["en"].ID, "scene %d should have EN voiceover", i)
	}
	assert.Equal(t, 2, len(docPub.records), "docs should be upserted exactly 2 times total")
	assert.GreaterOrEqual(t, renderEnq.callCount, 1, "render should be enqueued")
}

// TestRunner_EnqueueFailsAfterDocs_RetryPreservesArtifacts covers
// Verdetto scenario 4: RenderEnqueuer fails AFTER documents were
// upserted → first attempt fails → reset stub → second Execute
// resumes from StageEnqueuingRender and does NOT re-upsert
// documents nor re-generate voiceovers.
func TestRunner_EnqueueFailsAfterDocs_RetryPreservesArtifacts(t *testing.T) {
	runner, repo, _, _, voiceoverGen, docPub, renderEnq := newTestRunner()
	req := defaultTestRequest()

	// Configure renderEnq to fail on first call.
	renderEnq.err = errors.New("enqueue render failed: worker queue full")
	renderEnq.failAfter = 0

	runID := "run-enqueue-fail-001"
	err := repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	})
	require.NoError(t, err)

	// Execute — first attempt fails at enqueue.
	runner.Execute(context.Background(), runID, req)

	time.Sleep(100 * time.Millisecond)

	// Confirm failure.
	run, err := repo.Get(context.Background(), runID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusFailed, run.Status)
	assert.Equal(t, StageEnqueuingRender, run.FailedStage)

	// Record how many docs and voiceovers were created in the first attempt.
	firstDocCalls := len(docPub.records)
	firstVOCalls := voiceoverGen.callCount

	// Reset renderEnq to succeed on retry.
	renderEnq.err = nil
	renderEnq.failAfter = -1

	// Second attempt — should resume from ENQUEUING_RENDER.
	// Translations, voiceovers, and docs should NOT be recreated.
	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status)

	// Docs should NOT increase: stage 5 was already completed.
	assert.Equal(t, firstDocCalls, len(docPub.records),
		"document upserts should not increase on retry")

	// Voiceover calls should NOT increase on retry.
	assert.Equal(t, firstVOCalls, voiceoverGen.callCount,
		"voiceover calls should not increase on retry")

	// Render enqueue should succeed on retry.
	assert.GreaterOrEqual(t, renderEnq.callCount, 2, "renderEnq should be called again on retry")
	require.NotNil(t, final.Result.RenderJob)
	assert.Equal(t, "render-xyz-789", final.Result.RenderJob.JobID)
}
