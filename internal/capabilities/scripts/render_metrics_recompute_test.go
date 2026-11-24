package scriptgeneration

import (
	"testing"
	"time"
)

// TestRecomputeLocalizedRenderMetricsIgnoresDedupedReceipts pins the fix for the
// 2026-11-16 Obama runs: a resumed attempt re-emits render receipts, so the
// running accumulators described renders that the certified set no longer
// contains (35 cache-hit clips reported work_ms=1.351.641 and wall_ms=175.821
// against 0,62 s of real spans). The summary must describe the delivered clips.
func TestRecomputeLocalizedRenderMetricsIgnoresDedupedReceipts(t *testing.T) {
	base := time.Date(2026, 11, 16, 19, 14, 41, 0, time.UTC)
	first := LocalizedRenderResult{
		SceneID: "intro-01", ClipID: "clip-a", Language: "it",
		WallMS: 1000, QueueWaitMS: 250, StartedAt: base, FinishedAt: base.Add(time.Second),
	}
	second := LocalizedRenderResult{
		SceneID: "body-01", ClipID: "clip-b", Language: "it",
		WallMS: 400, QueueWaitMS: 150, StartedAt: base.Add(2 * time.Second), FinishedAt: base.Add(2400 * time.Millisecond),
	}
	// Receipts from an earlier, slower attempt followed by the certified ones:
	// dedupeLocalizedRenders keeps the newest receipt per identity, so only
	// `first` and `second` survive.
	renders := []LocalizedRenderResult{
		{SceneID: "intro-01", ClipID: "clip-a", Language: "it", WallMS: 40000, StartedAt: base.Add(-time.Hour), FinishedAt: base.Add(-time.Hour + 40*time.Second)},
		{SceneID: "body-01", ClipID: "clip-b", Language: "it", WallMS: 30000, StartedAt: base.Add(-time.Hour + 41*time.Second), FinishedAt: base.Add(-time.Hour + 71*time.Second)},
		first,
		second,
	}

	result := &GenerateResult{
		LocalizedRenders: dedupeLocalizedRenders(renders),
		RenderMetrics: &RenderMetrics{
			Expected: 2, Successful: 2,
			// Inflated by every receipt the attempt produced.
			WorkMS: 41400, WallMS: 3610000,
		},
	}

	recomputeLocalizedRenderMetrics(result)

	if got, want := len(result.LocalizedRenders), 2; got != want {
		t.Fatalf("deduped renders = %d, want %d", got, want)
	}
	if got, want := result.RenderMetrics.WorkMS, int64(1400); got != want {
		t.Errorf("work_ms = %d, want %d (sum of the certified receipts)", got, want)
	}
	if got, want := result.RenderMetrics.QueueWaitMS, int64(400); got != want {
		t.Errorf("queue_wait_ms = %d, want %d (admission wait is not work)", got, want)
	}
	if got, want := result.RenderMetrics.WallMS, int64(2400); got != want {
		t.Errorf("wall_ms = %d, want %d (first start of the certified set to last finish)", got, want)
	}
	if !result.RenderFirstStartedAt.Equal(base) {
		t.Errorf("first start = %v, want %v", result.RenderFirstStartedAt, base)
	}
}

// TestRecomputeLocalizedRenderMetricsEmptySetZeroesTheSummary keeps the
// no-render path honest: a result with no certified clips reports zero work and
// zero wall instead of the last attempt's stale accumulators.
func TestRecomputeLocalizedRenderMetricsEmptySetZeroesTheSummary(t *testing.T) {
	result := &GenerateResult{
		RenderMetrics: &RenderMetrics{WorkMS: 900000, WallMS: 120000},
	}
	recomputeLocalizedRenderMetrics(result)
	if result.RenderMetrics.WorkMS != 0 || result.RenderMetrics.WallMS != 0 || result.RenderMetrics.QueueWaitMS != 0 {
		t.Fatalf("empty set metrics = work:%d wall:%d queue:%d, want 0/0/0", result.RenderMetrics.WorkMS, result.RenderMetrics.WallMS, result.RenderMetrics.QueueWaitMS)
	}
}

// TestRecomputeLocalizedRenderMetricsNilResult is the defensive pin: lifecycle
// code calls the recompute unconditionally, so a nil result must be a no-op.
func TestRecomputeLocalizedRenderMetricsNilResult(t *testing.T) {
	recomputeLocalizedRenderMetrics(nil)
}
