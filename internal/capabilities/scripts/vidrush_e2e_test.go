// Package scriptgeneration — vidrush_e2e_test.go certifies the incremental
// VidRush pipeline end to end: while generation is still committing scenes,
// the coordinator enriches the previous scene, so a 5-scene run overlaps
// generation and VidRush enrichment. The test records a wall-clock timeline
// and asserts the success signal (overlap_ms > 0), canonical scene order,
// exactly-once enrichment, and that the final barrier waits only for the
// still-running enrichments.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// timelineRecorder captures ordered, wall-clock pipeline events for the E2E
// overlap assertion.
type timelineRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *timelineRecorder) record(name string) {
	r.mu.Lock()
	r.events = append(r.events, name)
	r.mu.Unlock()
}

func (r *timelineRecorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

// e2eBlockingEnricher is a SegmentEnricher that blocks until released and
// records its start/completion on the shared timeline. It records the start
// before the call is observable so a test that waits for the call count is
// guaranteed the start event is already on the timeline.
type e2eBlockingEnricher struct {
	mu       sync.Mutex
	calls    []scriptpkg.SpecScene
	release  chan struct{}
	timeline *timelineRecorder
}

func newE2EBlockingEnricher(timeline *timelineRecorder) *e2eBlockingEnricher {
	return &e2eBlockingEnricher{release: make(chan struct{}), timeline: timeline}
}

func (e *e2eBlockingEnricher) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

func (e *e2eBlockingEnricher) Enrich(ctx context.Context, _ *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	e.timeline.record("vidrush " + scene.ID + " started")
	e.mu.Lock()
	e.calls = append(e.calls, scene)
	e.mu.Unlock()
	select {
	case <-e.release:
	case <-ctx.Done():
		return scriptpkg.VidRushSegmentResult{}, ctx.Err()
	}
	e.timeline.record("vidrush " + scene.ID + " completed")
	return scriptpkg.VidRushSegmentResult{
		SegmentID: scene.ID,
		SceneID:   scene.ID,
		Position:  scene.Index,
		Text:      scene.Text,
		TextHash:  SceneTextHash(scene.Text),
	}, nil
}

// assertSequence asserts that event "before" appears earlier in names than
// event "after". Both must be present.
func assertSequence(t *testing.T, names []string, before, after string) {
	t.Helper()
	bi, ai := -1, -1
	for i, n := range names {
		if n == before {
			bi = i
		}
		if n == after {
			ai = i
		}
	}
	require.GreaterOrEqual(t, bi, 0, "expected event %q in timeline %v", before, names)
	require.GreaterOrEqual(t, ai, 0, "expected event %q in timeline %v", after, names)
	assert.Less(t, bi, ai, "expected %q before %q in timeline %v", before, after, names)
}

func TestIncrementalVidRush_E2E_FiveScenesOverlapGeneration(t *testing.T) {
	timeline := &timelineRecorder{}
	enricher := newE2EBlockingEnricher(timeline)
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)
	metrics := &recordingVidRushMetrics{}
	coordinator.SetMetrics(metrics)

	coordinator.MarkGenerationStart(time.Now())

	scenes := []struct {
		id    string
		index int
		text  string
	}{
		{"scene-0", 0, "Scene zero narration"},
		{"scene-1", 1, "Scene one narration"},
		{"scene-2", 2, "Scene two narration"},
		{"scene-3", 3, "Scene three narration"},
		{"scene-4", 4, "Scene four narration"},
	}

	// Generate scene-0, then let its enrichment start (and block) before
	// continuing to generate scenes 1..4. This is the overlap the pipeline
	// must produce: VidRush works on scene N while generation writes N+1.
	timeline.record("generate scene-0 start")
	commit(t, coordinator, "run-1", scenes[0].id, scenes[0].index, scenes[0].text, 1)
	timeline.record("generate scene-0 committed")

	deadline := time.Now().Add(2 * time.Second)
	for enricher.callCount() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("scene-0 enrichment did not start")
		}
		time.Sleep(time.Millisecond)
	}

	for i := 1; i < len(scenes); i++ {
		timeline.record("generate " + scenes[i].id + " start")
		commit(t, coordinator, "run-1", scenes[i].id, scenes[i].index, scenes[i].text, 1)
		timeline.record("generate " + scenes[i].id + " committed")
	}
	coordinator.MarkGenerationComplete(time.Now())
	timeline.record("generation complete")

	// Release enrichments and await the final barrier.
	close(enricher.release)
	results, err := coordinator.WaitForVidRush(context.Background(), "run-1")
	require.NoError(t, err)
	timeline.record("vidrush barrier complete")

	// ── Canonical order and exactly-once enrichment ────────────────────
	require.Len(t, results, 5, "all five scenes must be enriched")
	for i, r := range results {
		assert.Equal(t, i, r.Position, "results must be in canonical SceneIndex order")
		assert.Equal(t, scenes[i].id, r.SceneID)
		assert.Equal(t, scenes[i].text, r.Text)
	}
	assert.Equal(t, 5, enricher.callCount(), "each committed scene enriched exactly once")

	// ── Metrics ────────────────────────────────────────────────────────
	committed, started, completed, _, _, overlap := metrics.snapshot()
	assert.Equal(t, 5, committed, "one committed metric per scene")
	assert.Equal(t, 5, started, "one enrichment-started metric per scene")
	assert.Equal(t, 5, completed, "one enrichment-completed metric per scene")
	assert.Greater(t, overlap, 0.0, "generation overlap must be positive")

	// ── Timing success signal: overlap_ms > 0 ─────────────────────────
	timings := coordinator.RunTimings()
	assert.True(t, timings.OverlapAchieved(), "overlap_ms must be > 0")
	assert.Greater(t, timings.OverlapMS, int64(0))
	assert.Greater(t, timings.GenerationTotalMS, int64(0))
	assert.GreaterOrEqual(t, timings.BarrierWaitMS, int64(0))
	assert.GreaterOrEqual(t, timings.VidRushTotalMS, int64(0))

	// ── Timeline: enrichment of scene-0 began while generation continued ──
	names := timeline.names()
	assertSequence(t, names, "generate scene-0 committed", "generate scene-1 start")
	assertSequence(t, names, "vidrush scene-0 started", "generate scene-1 committed")
	assertSequence(t, names, "vidrush scene-0 started", "generation complete")
	assertSequence(t, names, "generation complete", "vidrush barrier complete")
	assertSequence(t, names, "vidrush scene-0 completed", "vidrush barrier complete")
}

func TestIncrementalVidRush_E2E_BarrierWaitsOnlyForPendingScenes(t *testing.T) {
	timeline := &timelineRecorder{}
	enricher := newE2EBlockingEnricher(timeline)
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	commit(t, coordinator, "run-1", "scene-0", 0, "Scene zero narration", 1)
	commit(t, coordinator, "run-1", "scene-1", 1, "Scene one narration", 1)

	waitResult := make(chan error, 1)
	go func() {
		_, err := coordinator.WaitForVidRush(context.Background(), "run-1")
		waitResult <- err
	}()

	// The barrier must block while enrichments are still pending.
	select {
	case err := <-waitResult:
		t.Fatalf("barrier returned before pending scenes completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	// The barrier must not re-run enrichment: each scene was enriched exactly
	// once so far (the blocked first extraction plus the queued second one).
	assert.LessOrEqual(t, enricher.callCount(), 2, "barrier must not re-run enrichment while pending")

	close(enricher.release)
	select {
	case err := <-waitResult:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("barrier did not complete after pending scenes finished")
	}
	assert.Equal(t, 2, enricher.callCount(), "each scene enriched exactly once, never re-run")
}
