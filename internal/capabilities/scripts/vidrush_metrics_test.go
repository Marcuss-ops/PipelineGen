// Package scriptgeneration — vidrush_metrics_test.go certifies the bounded
// VidRush metrics surface and the per-run generation↔VidRush overlap timing:
// committed/started/completed counters fire once per scene, stale results are
// counted, and a positive overlap (enrichment began before generation
// finished) is the success signal.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingVidRushMetrics is a bounded per-scene metrics recorder fake. It
// captures call counts and the last barrier/overlap samples for assertion.
type recordingVidRushMetrics struct {
	mu          sync.Mutex
	committed   int
	started     int
	completed   int
	barrierWait float64
	overlap     float64
	stale       int
}

func (m *recordingVidRushMetrics) SceneCommitted() {
	m.mu.Lock()
	m.committed++
	m.mu.Unlock()
}
func (m *recordingVidRushMetrics) EnrichmentStarted() {
	m.mu.Lock()
	m.started++
	m.mu.Unlock()
}
func (m *recordingVidRushMetrics) EnrichmentCompleted(time.Duration) {
	m.mu.Lock()
	m.completed++
	m.mu.Unlock()
}
func (m *recordingVidRushMetrics) BarrierWait(seconds float64) {
	m.mu.Lock()
	m.barrierWait = seconds
	m.mu.Unlock()
}
func (m *recordingVidRushMetrics) GenerationOverlap(seconds float64) {
	m.mu.Lock()
	m.overlap = seconds
	m.mu.Unlock()
}
func (m *recordingVidRushMetrics) StaleResult() {
	m.mu.Lock()
	m.stale++
	m.mu.Unlock()
}

func (m *recordingVidRushMetrics) snapshot() (committed, started, completed, stale int, barrierWait, overlap float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.committed, m.started, m.completed, m.stale, m.barrierWait, m.overlap
}

// recordingTimingRecorder is a VidRushTimingRecorder fake for the runner.
type recordingTimingRecorder struct {
	mu       sync.Mutex
	start    time.Time
	complete time.Time
}

func (r *recordingTimingRecorder) MarkGenerationStart(t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.start.IsZero() {
		r.start = t
	}
}
func (r *recordingTimingRecorder) MarkGenerationComplete(t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.complete.IsZero() || t.After(r.complete) {
		r.complete = t
	}
}
func (r *recordingTimingRecorder) RunTimings() VidRushRunTimings {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := VidRushRunTimings{}
	if !r.start.IsZero() && !r.complete.IsZero() {
		t.GenerationTotalMS = r.complete.Sub(r.start).Milliseconds()
	}
	return t
}

func TestVidRushRunTimings_OverlapAchieved(t *testing.T) {
	assert.False(t, (VidRushRunTimings{}).OverlapAchieved(), "zero overlap must not be a success")
	assert.False(t, (VidRushRunTimings{OverlapMS: 0}).OverlapAchieved(), "zero overlap must not be a success")
	assert.True(t, (VidRushRunTimings{OverlapMS: 1}).OverlapAchieved(), "positive overlap is the success signal")
}

func TestIncrementalCoordinator_EmitsSceneMetrics(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)
	metrics := &recordingVidRushMetrics{}
	coordinator.SetMetrics(metrics)

	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)
	commit(t, coordinator, "run-1", "scene-1", 1, "Second scene text", 1)
	commit(t, coordinator, "run-1", "scene-2", 2, "Third scene text", 1)

	results, err := coordinator.Wait(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 3)

	committed, started, completed, stale, barrierWait, _ := metrics.snapshot()
	assert.Equal(t, 3, committed, "one committed metric per stable scene")
	assert.Equal(t, 3, started, "one enrichment-started metric per scene")
	assert.Equal(t, 3, completed, "one enrichment-completed metric per scene")
	assert.Equal(t, 0, stale, "no stale results on a clean run")
	assert.GreaterOrEqual(t, barrierWait, 0.0, "barrier wait must be sampled")
}

func TestIncrementalCoordinator_EmitsStaleResultMetric(t *testing.T) {
	enricher := newBlockingSegmentEnricher()
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)
	metrics := &recordingVidRushMetrics{}
	coordinator.SetMetrics(metrics)

	commit(t, coordinator, "run-1", "scene-0", 0, "Old scene text", 1)
	commit(t, coordinator, "run-1", "scene-0", 0, "New scene text", 2)
	close(enricher.release)

	_, err := coordinator.Wait(context.Background())
	require.NoError(t, err)

	_, _, _, stale, _, _ := metrics.snapshot()
	assert.Equal(t, 1, stale, "the fenced revision-1 result must be counted as stale")
	assert.Equal(t, 1, coordinator.StaleResults())
}

func TestIncrementalCoordinator_RunTimings_OverlapPositive(t *testing.T) {
	enricher := newBlockingSegmentEnricher()
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)
	metrics := &recordingVidRushMetrics{}
	coordinator.SetMetrics(metrics)

	coordinator.MarkGenerationStart(time.Now())
	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)

	// Wait until the enrichment has actually started (the blocking enricher
	// records the call after firstEnrichmentStart is set), then close the
	// generation window afterwards so overlap is deterministically positive.
	deadline := time.Now().Add(2 * time.Second)
	for enricher.callCount() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("enrichment did not start")
		}
		time.Sleep(time.Millisecond)
	}
	coordinator.MarkGenerationComplete(time.Now())
	close(enricher.release)

	results, err := coordinator.Wait(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)

	_, _, _, _, _, overlap := metrics.snapshot()
	assert.Greater(t, overlap, 0.0, "overlap must be positive when enrichment began before generation finished")

	timings := coordinator.RunTimings()
	assert.True(t, timings.OverlapAchieved(), "positive overlap is the success signal")
	assert.Greater(t, timings.OverlapMS, int64(0))
	assert.Greater(t, timings.GenerationTotalMS, int64(0), "generation window must be measured")
	assert.GreaterOrEqual(t, timings.VidRushTotalMS, int64(0))
	assert.GreaterOrEqual(t, timings.BarrierWaitMS, int64(0))
}

func TestIncrementalCoordinator_RunTimings_NoOverlapWhenSequential(t *testing.T) {
	enricher := newBlockingSegmentEnricher()
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)

	// Generation completes before any scene is committed: the enrichment can
	// only start after generation has already finished, so overlap is zero.
	coordinator.MarkGenerationStart(time.Now())
	coordinator.MarkGenerationComplete(time.Now())
	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)
	close(enricher.release)

	_, err := coordinator.Wait(context.Background())
	require.NoError(t, err)

	timings := coordinator.RunTimings()
	assert.False(t, timings.OverlapAchieved(), "enrichment started only after generation finished → no overlap")
	assert.Equal(t, int64(0), timings.OverlapMS)
}

func TestRunner_ReportsGenerationWindowToTimingRecorder(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	rec := &recordingTimingRecorder{}
	runner.SetVidRushTimingRecorder(rec)

	req := defaultTestRequest()
	runID := "run-timing-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status)

	rec.mu.Lock()
	start, complete := rec.start, rec.complete
	rec.mu.Unlock()

	assert.False(t, start.IsZero(), "generation start must be recorded")
	assert.False(t, complete.IsZero(), "generation complete must be recorded")
	assert.False(t, complete.Before(start), "generation complete must not precede start")
	assert.GreaterOrEqual(t, rec.RunTimings().GenerationTotalMS, int64(0))
}
