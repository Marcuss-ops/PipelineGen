// Package scriptgeneration — incremental_coordinator_test.go certifies the
// VidRushIncrementalCoordinator contract: each committed scene is enriched
// exactly once, results merge in canonical order, stale text-hash results are
// fenced out, the barrier waits only for pending scenes, and enrichment
// failures are attributed to the correct scene.
package scriptgeneration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// fakeSegmentEnricher records the scenes it enriches and returns a result
// whose TextHash is derived from the committed scene text, mirroring the
// canonical identity contract.
type fakeSegmentEnricher struct {
	mu    sync.Mutex
	calls []scriptpkg.SpecScene
	errs  map[string]error
}

func (f *fakeSegmentEnricher) Enrich(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, scene)
	err := f.errs[scene.ID]
	f.mu.Unlock()
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	return scriptpkg.VidRushSegmentResult{
		SegmentID: scene.ID,
		SceneID:   scene.ID,
		Position:  scene.Index,
		Text:      scene.Text,
		TextHash:  SceneTextHash(scene.Text),
	}, nil
}

func (f *fakeSegmentEnricher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// blockingSegmentEnricher holds enrichments until released, letting tests
// commit superseding scenes before any enrichment result is recorded.
type blockingSegmentEnricher struct {
	mu      sync.Mutex
	calls   []scriptpkg.SpecScene
	release chan struct{}
}

func newBlockingSegmentEnricher() *blockingSegmentEnricher {
	return &blockingSegmentEnricher{release: make(chan struct{})}
}

func (b *blockingSegmentEnricher) Enrich(ctx context.Context, _ *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	b.mu.Lock()
	b.calls = append(b.calls, scene)
	b.mu.Unlock()
	select {
	case <-b.release:
	case <-ctx.Done():
		return scriptpkg.VidRushSegmentResult{}, ctx.Err()
	}
	return scriptpkg.VidRushSegmentResult{
		SegmentID: scene.ID,
		SceneID:   scene.ID,
		Position:  scene.Index,
		Text:      scene.Text,
		TextHash:  SceneTextHash(scene.Text),
	}, nil
}

func commit(t *testing.T, c *VidRushIncrementalCoordinator, runID, sceneID string, index int, text string, revision int64) {
	t.Helper()
	err := c.OnSceneCommitted(context.Background(), SceneCommitted{
		RunID: runID, SceneID: sceneID, SceneIndex: index,
		Text: text, TextHash: SceneTextHash(text), Revision: revision, Language: "en",
	})
	require.NoError(t, err)
}

func TestIncrementalCoordinator_EachCommittedSceneProcessedExactlyOnce(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)
	commit(t, coordinator, "run-1", "scene-1", 1, "Second scene text", 1)
	commit(t, coordinator, "run-1", "scene-2", 2, "Third scene text", 1)

	results, err := coordinator.Wait(context.Background())
	require.NoError(t, err)
	assert.Len(t, results, 3)
	assert.Equal(t, 3, enricher.callCount(), "each committed scene must be enriched exactly once")
	assert.Equal(t, 0, coordinator.StaleResults())
}

func TestIncrementalCoordinator_PreservesCanonicalSceneOrder(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	// Commit out of order; the barrier must still return canonical order.
	commit(t, coordinator, "run-1", "scene-2", 2, "Third scene text", 1)
	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)
	commit(t, coordinator, "run-1", "scene-1", 1, "Second scene text", 1)

	results, err := coordinator.Wait(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, []string{"scene-0", "scene-1", "scene-2"}, []string{results[0].SceneID, results[1].SceneID, results[2].SceneID})
}

func TestIncrementalCoordinator_DiscardsStaleTextHashResults(t *testing.T) {
	enricher := newBlockingSegmentEnricher()
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	// Commit scene-0 at revision 1, then supersede it at revision 2 before
	// either enrichment can record its result.
	commit(t, coordinator, "run-1", "scene-0", 0, "Old scene text", 1)
	commit(t, coordinator, "run-1", "scene-0", 0, "New scene text", 2)
	close(enricher.release)

	results, err := coordinator.Wait(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "New scene text", results[0].Text)
	assert.Equal(t, SceneTextHash("New scene text"), results[0].TextHash)
	assert.Equal(t, 1, coordinator.StaleResults(), "the superseded revision-1 result must be fenced out")
}

func TestIncrementalCoordinator_FinalBarrierWaitsOnlyForPendingScenes(t *testing.T) {
	enricher := newBlockingSegmentEnricher()
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)
	commit(t, coordinator, "run-1", "scene-1", 1, "Second scene text", 1)

	waitResult := make(chan error, 1)
	go func() {
		_, err := coordinator.Wait(context.Background())
		waitResult <- err
	}()

	// The barrier must block while enrichments are still pending.
	select {
	case err := <-waitResult:
		t.Fatalf("Wait returned before pending scenes completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(enricher.release)
	select {
	case err := <-waitResult:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not complete after pending scenes finished")
	}
}

func TestIncrementalCoordinator_FailureAttributedToCorrectScene(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{"scene-1": errors.New("extraction exploded")}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)
	commit(t, coordinator, "run-1", "scene-1", 1, "Second scene text", 1)

	_, err := coordinator.Wait(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scene-1")
	assert.Contains(t, err.Error(), "extraction exploded")
}
