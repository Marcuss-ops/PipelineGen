package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func translationCheckpointRunner(t *testing.T) (*Runner, *countingRunRepository, *memoryArtifactCheckpointRepository) {
	t.Helper()
	runID := "run-translation-checkpoint"
	inner := newInMemRunRepository()
	require.NoError(t, inner.Create(context.Background(), &GenerationRun{ID: runID, Result: &GenerateResult{}}))
	runRepo := &countingRunRepository{inMemRunRepository: inner}
	artifactRepo := newMemoryArtifactCheckpointRepository()
	runner := NewRunner(runRepo, nil, nil, nil)
	runner.SetPhraseAligner(testPhraseAligner{})
	runner.SetArtifactCheckpointRepository(artifactRepo)
	runner.SetArtifactCheckpointDualWrite(true)
	runner.SetArtifactCheckpointResume(true)
	return runner, runRepo, artifactRepo
}

// TestTranslationCheckpointRoundTrip pins the self-verifying text contract:
// the row stores the translated text, its digest and the source/target input
// fingerprint; a changed source text is not reusable.
func TestTranslationCheckpointRoundTrip(t *testing.T) {
	ctx := context.Background()
	runner, _, artifactRepo := translationCheckpointRunner(t)
	const runID = "run-translation-checkpoint"

	require.NoError(t, runner.saveTranslationArtifactCheckpoint(ctx, runID, 1, "scene-1", "en", "it", "Hello world", "Ciao mondo"))
	require.Equal(t, 1, artifactRepo.count())

	got, ok := runner.loadTranslationCheckpoint(ctx, runID, "scene-1", "en", "it", "Hello world")
	require.True(t, ok)
	require.Equal(t, "Ciao mondo", got)

	got, ok = runner.loadTranslationCheckpoint(ctx, runID, "scene-1", "en", "it", "Changed source")
	require.False(t, ok, "a changed source text must invalidate the translation")
	require.Empty(t, got)
	require.Zero(t, artifactRepo.count(), "the stale row is deleted, not silently reused")
}

// TestTranslationCutoverSkipsOnlyTheUnitEnvelope proves the cutover boundary:
// the per-unit debounced write is the one skipped, while the post-fan-out
// stage checkpoint still persists the whole result.
func TestTranslationCutoverSkipsOnlyTheUnitEnvelope(t *testing.T) {
	ctx := context.Background()
	runner, runRepo, _ := translationCheckpointRunner(t)
	const runID = "run-translation-checkpoint"
	runner.SetArtifactCheckpointCutover(true)

	snapshot := &GenerateResult{Scenes: []Scene{{ID: "scene-1", Index: 0, Text: map[Language]string{"en": "Hello"}}}}
	require.NoError(t, runner.saveTranslationUnitEnvelope(ctx, runID, snapshot))
	require.Zero(t, runRepo.saves, "the per-unit envelope write must be skipped during cutover")

	require.NoError(t, runner.checkpoint(ctx, runID, snapshot))
	require.Equal(t, 1, runRepo.saves, "the stage boundary remains the whole-result owner")
}

// TestTranslationPhaseRestoresCheckpointWithoutProviderCall is the loader's
// end-to-end seam: a scene whose translation is absent from the envelope is
// restored from the artifact row before the work list is built, so the
// translator is not called again.
func TestTranslationPhaseRestoresCheckpointWithoutProviderCall(t *testing.T) {
	ctx := context.Background()
	runner, repo, _, translator, _, _ := newTestRunner()
	const runID = "run-translation-restore"
	artifactRepo := newMemoryArtifactCheckpointRepository()
	runner.SetArtifactCheckpointRepository(artifactRepo)
	runner.SetArtifactCheckpointDualWrite(true)
	runner.SetArtifactCheckpointResume(true)

	req := GenerateRequest{SourceLanguage: "en", Languages: []Language{"it"}}
	require.NoError(t, repo.Create(ctx, &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	require.NoError(t, runner.saveTranslationArtifactCheckpoint(ctx, runID, 1, "scene-1", "en", "it", "Hello world", "Ciao mondo"))

	result := &GenerateResult{Scenes: []Scene{{
		ID: "scene-1", Index: 0,
		Text: map[Language]string{"en": "Hello world"},
	}}}
	require.True(t, runner.runTranslationPhase(ctx, runID, req, NewExecutionContext(runID, "restore"), -1, result))
	require.Equal(t, "Ciao mondo", result.Scenes[0].Text["it"])
	require.Zero(t, translator.callCounts["scene-1"], "the restored translation must not call the provider")
	require.False(t, strings.TrimSpace(result.Scenes[0].Text["it"]) == "")
}
