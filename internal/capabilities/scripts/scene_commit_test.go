// Package scriptgeneration — scene_commit_test.go certifies the
// SceneCommitted boundary: every stable scene is reported exactly once, in
// canonical order, with a deterministic content hash; an observer failure is
// fail-closed; and the text hash is content-sensitive.
package scriptgeneration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingSceneCommitObserver collects SceneCommitted events in arrival
// order and supports fault injection via err.
type recordingSceneCommitObserver struct {
	mu     sync.Mutex
	events []SceneCommitted
	err    error
}

func (o *recordingSceneCommitObserver) OnSceneCommitted(_ context.Context, event SceneCommitted) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err != nil {
		return o.err
	}
	o.events = append(o.events, event)
	return nil
}

func (o *recordingSceneCommitObserver) committed() []SceneCommitted {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]SceneCommitted(nil), o.events...)
}

func TestSceneCommitted_EmittedOncePerSceneInCanonicalOrder(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	observer := &recordingSceneCommitObserver{}
	runner.SetSceneCommitObserver(observer)
	req := defaultTestRequest()

	runID := "run-commit-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)

	events := observer.committed()
	require.Len(t, events, 3, "one commit per scene")

	wantScenes := defaultTestScenes()
	for i, event := range events {
		assert.Equal(t, runID, event.RunID)
		assert.Equal(t, i, event.SceneIndex)
		assert.Equal(t, wantScenes[i].ID, event.SceneID)
		assert.Equal(t, wantScenes[i].Text["en"], event.Text)
		assert.Equal(t, SceneTextHash(wantScenes[i].Text["en"]), event.TextHash)
		assert.Equal(t, int64(1), event.Revision)
		assert.Equal(t, "en", event.Language)
	}
}

func TestSceneCommitted_TextHashIsDeterministicAndContentSensitive(t *testing.T) {
	first := SceneTextHash("First scene text")
	assert.Equal(t, first, SceneTextHash("First scene text"), "hash must be deterministic")

	// Cosmetic whitespace/casing differences must not change the identity.
	assert.Equal(t, first, SceneTextHash("  first   SCENE TEXT\n"), "whitespace/casing normalization")

	// A real content change must change the identity (stale-result fence).
	assert.NotEqual(t, first, SceneTextHash("First scene text changed"))
}

func TestSceneCommitted_ObserverErrorFailsRunClosed(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runner.SetSceneCommitObserver(&recordingSceneCommitObserver{err: errors.New("scene commit observer failure")})
	req := defaultTestRequest()

	runID := "run-commit-fail-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusFailed, final.Status)
	assert.Equal(t, StageGeneratingSceneText, final.FailedStage)
}
