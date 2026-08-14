// Package scriptgeneration — vidrush_metrics.go owns the VidRush observability
// contract: the bounded per-scene metrics port consumed by the incremental
// coordinator, and the per-run timing surface (generation vs VidRush overlap)
// used to prove that enrichment overlaps generation instead of running after it.
package scriptgeneration

import "time"

// VidRushMetrics records bounded per-scene pipeline events for the incremental
// VidRush coordinator. Dynamic identifiers (run id, scene id, segment id,
// asset id) belong in structured logs, never in metric labels, so this port
// carries no labels.
type VidRushMetrics interface {
	// SceneCommitted records one stable scene committed by the runner.
	SceneCommitted()
	// EnrichmentStarted records one scene enrichment beginning.
	EnrichmentStarted()
	// EnrichmentCompleted records one scene enrichment finishing, with the
	// wall-clock duration of that scene's enrichment.
	EnrichmentCompleted(duration time.Duration)
	// BarrierWait records the wall-clock time the final barrier spent waiting
	// for still-running enrichments.
	BarrierWait(seconds float64)
	// GenerationOverlap records the wall-clock overlap between scene
	// generation and VidRush enrichment for the run. overlap > 0 is the
	// success signal for the incremental pipeline.
	GenerationOverlap(seconds float64)
	// StaleResult records one enrichment result discarded by stale-result
	// fencing (superseded text hash or revision).
	StaleResult()
}

// VidRushRunTimings is the per-run wall-clock timing contract. A positive
// OverlapMS proves enrichment began before generation finished — the success
// signal for the incremental VidRush pipeline.
type VidRushRunTimings struct {
	// GenerationTotalMS is the total wall-clock time spent generating scene
	// text (from generation start to the last scene commit).
	GenerationTotalMS int64 `json:"generation_total_ms"`
	// VidRushTotalMS is the total wall-clock time from the first enrichment
	// start to the final barrier completion.
	VidRushTotalMS int64 `json:"vidrush_total_ms"`
	// BarrierWaitMS is the wall-clock time the final barrier waited for
	// still-running enrichments.
	BarrierWaitMS int64 `json:"vidrush_barrier_wait_ms"`
	// OverlapMS is the wall-clock overlap between generation and VidRush
	// enrichment. It is zero when enrichment only starts after generation has
	// fully completed (the sequential-block anti-pattern).
	OverlapMS int64 `json:"overlap_ms"`
}

// OverlapAchieved reports whether the run achieved incremental overlap, i.e.
// VidRush enrichment began before scene generation finished.
func (t VidRushRunTimings) OverlapAchieved() bool {
	return t.OverlapMS > 0
}

// VidRushTimingRecorder is the seam through which the runner reports the
// scene-generation wall-clock window so the coordinator can compute the
// generation↔VidRush overlap (the success signal: overlap_ms > 0).
type VidRushTimingRecorder interface {
	// MarkGenerationStart records the moment scene-text generation began.
	MarkGenerationStart(t time.Time)
	// MarkGenerationComplete records the moment the last scene was committed
	// (generation finished emitting stable scenes).
	MarkGenerationComplete(t time.Time)
	// RunTimings returns the per-run wall-clock timing surface. OverlapMS is
	// populated once both the generation window and the first enrichment start
	// are known.
	RunTimings() VidRushRunTimings
}
