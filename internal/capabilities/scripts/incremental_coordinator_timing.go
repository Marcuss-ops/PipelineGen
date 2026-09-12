// Package scriptgeneration — incremental_coordinator_timing.go: the timing
// surface of the VidRushIncrementalCoordinator.
//
// It owns the generation start/complete marks, the enrichment-start mark and
// the RunTimings projection (including the generation/VidRush overlap), so the
// coordinator's orchestration file stays free of clock arithmetic.
//
// Extracted 2026-09-12 from incremental_coordinator.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

import "time"

// SetMetrics wires the bounded per-scene VidRush metrics recorder. A nil
// recorder is safe and disables emission.
func (c *VidRushIncrementalCoordinator) SetMetrics(metrics VidRushMetrics) {
	if c != nil {
		c.metrics = metrics
	}
}

// MarkGenerationStart records the moment scene-text generation began. Only
// the first mark wins (resume/retry must not reset an earlier window).
func (c *VidRushIncrementalCoordinator) MarkGenerationStart(t time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.genStart.IsZero() {
		c.genStart = t
	}
	c.mu.Unlock()
}

// MarkGenerationComplete records the moment generation finished emitting
// stable scenes. The latest mark wins.
func (c *VidRushIncrementalCoordinator) MarkGenerationComplete(t time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.genEnd.IsZero() || t.After(c.genEnd) {
		c.genEnd = t
	}
	c.mu.Unlock()
}

// markEnrichmentStart records the first time an enrichment goroutine began
// actual work. It is called from enrichment goroutines; only the first wins.
func (c *VidRushIncrementalCoordinator) markEnrichmentStart() {
	c.mu.Lock()
	if c.firstEnrichmentStart.IsZero() {
		c.firstEnrichmentStart = time.Now()
	}
	c.mu.Unlock()
}

// RunTimings returns the per-run wall-clock timing surface. It is safe to
// call after the barrier has completed (before that, barrier fields are zero
// and VidRushTotalMS/BarrierWaitMS are not yet known).
func (c *VidRushIncrementalCoordinator) RunTimings() VidRushRunTimings {
	if c == nil {
		return VidRushRunTimings{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t := VidRushRunTimings{}
	if !c.genStart.IsZero() && !c.genEnd.IsZero() {
		t.GenerationTotalMS = durationMilliseconds(c.genEnd.Sub(c.genStart))
	}
	if !c.firstEnrichmentStart.IsZero() && !c.barrierEnd.IsZero() {
		t.VidRushTotalMS = durationMilliseconds(c.barrierEnd.Sub(c.firstEnrichmentStart))
	}
	if !c.barrierStart.IsZero() && !c.barrierEnd.IsZero() {
		t.BarrierWaitMS = durationMilliseconds(c.barrierEnd.Sub(c.barrierStart))
	}
	t.OverlapMS = c.overlapMSLocked()
	return t
}

// overlapMSLocked computes the generation↔VidRush overlap. It is non-zero
// only when the first enrichment began before generation finished. Callers
// must hold c.mu.
func (c *VidRushIncrementalCoordinator) overlapMSLocked() int64 {
	if c.firstEnrichmentStart.IsZero() || c.genEnd.IsZero() {
		return 0
	}
	if !c.firstEnrichmentStart.Before(c.genEnd) {
		return 0
	}
	return durationMilliseconds(c.genEnd.Sub(c.firstEnrichmentStart))
}

// durationMilliseconds rounds a positive duration up to one millisecond. The
// timing contract uses a positive value as the overlap success signal; flooring
// sub-millisecond test and fast-path windows to zero would misreport real
// overlap as sequential execution.
func durationMilliseconds(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	ms := d / time.Millisecond
	if d%time.Millisecond != 0 {
		ms++
	}
	return int64(ms)
}
