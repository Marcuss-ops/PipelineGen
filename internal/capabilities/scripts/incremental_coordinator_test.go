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

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
		Insights: scriptpkg.SegmentInsights{
			SegmentID:      scene.ID,
			TextHash:       SceneTextHash(scene.Text),
			Entities:       []scriptpkg.ExtractedEntity{{Value: "subject", Type: "CONCEPT"}},
			ArtlistQueries: []string{"subject visual"},
			ImageQueries:   []string{"subject image"},
		},
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

func (b *blockingSegmentEnricher) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
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
		Insights:  scriptpkg.SegmentInsights{SegmentID: scene.ID, TextHash: SceneTextHash(scene.Text)},
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

func TestIncrementalCoordinator_RejectsMismatchedResultIdentity(t *testing.T) {
	event := SceneCommitted{SceneID: "latte-art", SceneIndex: 1, Text: "A barista makes latte art.", TextHash: SceneTextHash("A barista makes latte art."), Revision: 1}
	result := scriptpkg.VidRushSegmentResult{
		SegmentID: "coastal-road", SceneID: "coastal-road", Position: 0,
		Text: "Aerial coastal road.", TextHash: SceneTextHash("Aerial coastal road."),
		Insights: scriptpkg.SegmentInsights{SegmentID: "coastal-road", TextHash: SceneTextHash("Aerial coastal road.")},
	}
	if err := validateVidRushResultIdentity(event, result); err == nil {
		t.Fatal("expected mismatched scene identity to be rejected")
	}
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

func TestIncrementalCoordinator_DiscardsStaleRevisionResults(t *testing.T) {
	enricher := newBlockingSegmentEnricher()
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	// Same text, but the scene was regenerated at a higher revision. The
	// revision-1 enrichment must be fenced even though the text hash matches.
	commit(t, coordinator, "run-1", "scene-0", 0, "Same scene text", 1)
	commit(t, coordinator, "run-1", "scene-0", 0, "Same scene text", 2)
	close(enricher.release)

	results, err := coordinator.Wait(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Same scene text", results[0].Text)
	assert.Equal(t, 1, coordinator.StaleResults(), "the superseded revision-1 result must be fenced even with identical text")
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

func TestIncrementalCoordinator_WaitForVidRushWaitsOnlyForPendingScenes(t *testing.T) {
	enricher := newBlockingSegmentEnricher()
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)
	commit(t, coordinator, "run-1", "scene-1", 1, "Second scene text", 1)

	waitResult := make(chan error, 1)
	go func() {
		_, err := coordinator.WaitForVidRush(context.Background(), "run-1")
		waitResult <- err
	}()

	// The barrier must block while enrichments are still pending.
	select {
	case err := <-waitResult:
		t.Fatalf("WaitForVidRush returned before pending scenes completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(enricher.release)
	select {
	case err := <-waitResult:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForVidRush did not complete after pending scenes finished")
	}

	// Enricher must have been called exactly once per committed scene — the
	// barrier never re-runs whole-document extraction.
	assert.Equal(t, 2, enricher.callCount(), "barrier must not re-run enrichment")
}

func TestIncrementalCoordinator_WaitForVidRushReturnsCanonicalOrder(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	commit(t, coordinator, "run-1", "scene-2", 2, "Third scene text", 1)
	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)
	commit(t, coordinator, "run-1", "scene-1", 1, "Second scene text", 1)

	results, err := coordinator.WaitForVidRush(context.Background(), "run-1")
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, []string{"scene-0", "scene-1", "scene-2"}, []string{results[0].SceneID, results[1].SceneID, results[2].SceneID})
}

func TestIncrementalCoordinator_WaitForVidRushRejectsForeignRun(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)

	_, err := coordinator.WaitForVidRush(context.Background(), "run-2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run-2")
}

func TestIncrementalCoordinator_WaitForVidRushRequiresRunID(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)

	_, err := coordinator.WaitForVidRush(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing run id")
}

func TestIncrementalCoordinator_RejectsCommitFromForeignRun(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)

	err := coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
		RunID: "run-2", SceneID: "scene-1", SceneIndex: 1,
		Text: "Second scene text", TextHash: SceneTextHash("Second scene text"), Revision: 1, Language: "en",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run-2")
}

// fakeSegmentProviderResolver records the enriched segment it receives and
// returns it with one candidate asset merged.
type fakeSegmentProviderResolver struct {
	mu              sync.Mutex
	calls           int
	lastHadEntities bool
}

func (f *fakeSegmentProviderResolver) ResolveProviders(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastHadEntities = len(segment.Insights.Entities) > 0
	out := segment
	out.Assets.Candidates = append(out.Assets.Candidates, scriptpkg.SegmentAssetCandidate{AssetID: "asset-1", Provider: "artlist"})
	return out, nil
}

func (f *fakeSegmentProviderResolver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestIncrementalCoordinator_ProviderFanoutStartsAfterEntities(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	resolver := &fakeSegmentProviderResolver{}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)
	coordinator.SetSegmentProviderResolver(resolver)

	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)

	results, err := coordinator.Wait(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, 1, resolver.callCount(), "provider fanout must run once per enriched scene")
	assert.True(t, resolver.lastHadEntities, "provider fanout must start only after entity extraction")
	require.NotEmpty(t, results[0].Assets.Candidates, "fanout candidates must be merged into the immutable result")
	assert.Equal(t, "asset-1", results[0].Assets.Candidates[0].AssetID)
}

func TestIncrementalCoordinator_DoesNotMutateSharedSceneConcurrently(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 8)

	scenes := []struct {
		id    string
		index int
		text  string
	}{
		{"scene-0", 0, "First scene text"},
		{"scene-1", 1, "Second scene text"},
		{"scene-2", 2, "Third scene text"},
	}

	// Commit and enrich concurrently; the coordinator must never let one
	// scene's enrichment write into another scene's result or into any shared
	// scene envelope.
	var wg sync.WaitGroup
	for _, s := range scenes {
		wg.Add(1)
		go func(id string, idx int, text string) {
			defer wg.Done()
			err := coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
				RunID: "run-1", SceneID: id, SceneIndex: idx,
				Text: text, TextHash: SceneTextHash(text), Revision: 1, Language: "en",
			})
			if err != nil {
				t.Errorf("OnSceneCommitted(%s): %v", id, err)
			}
		}(s.id, s.index, s.text)
	}
	wg.Wait()

	results, err := coordinator.Wait(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 3)

	byID := make(map[string]string, len(results))
	for _, r := range results {
		byID[r.SceneID] = r.Text
	}
	for _, s := range scenes {
		if byID[s.id] != s.text {
			t.Fatalf("scene %s text = %q, want %q (concurrent contamination or mutation)", s.id, byID[s.id], s.text)
		}
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
