package observability

// concurrency_sweep.go owns the canonical deterministic concurrency sweep
// shared by every derivation in the codebase (batch runs, per-phase
// operations, fan-out trackers). One tie-breaker rule, defined once.
//
// TIE-BREAKER: at equal timestamps, finishes (-1) sort BEFORE starts (+1),
// so a completed slot is always freed before a new one is counted and the
// result NEVER depends on the input ordering of the intervals. This is the
// single definition of "peak / average concurrency": two events at the same
// instant can never produce a phantom overlap.

import (
	"sort"
	"time"
)

// concurrencyPoint is one boundary event of a concurrency interval: a start
// (+1) or a finish (-1) at an instant.
type concurrencyPoint struct {
	at    time.Time
	delta int
}

// sweepConcurrency computes the peak and time-weighted average concurrency
// over the given boundary points. wallMs is the span the average is weighted
// over (the caller's phase wall — for a well-formed interval set it equals
// last point − first point). The sort applies the canonical tie-breaker, so
// the result is deterministic for any input ordering.
func sweepConcurrency(points []concurrencyPoint, wallMs int64) (peak int, avg float64) {
	if len(points) == 0 {
		return 0, 0
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].at.Equal(points[j].at) {
			return points[i].delta < points[j].delta
		}
		return points[i].at.Before(points[j].at)
	})
	var weighted int64
	active := 0
	last := points[0].at
	for _, p := range points {
		weighted += int64(active) * p.at.Sub(last).Milliseconds()
		active += p.delta
		if active > peak {
			peak = active
		}
		last = p.at
	}
	if wallMs > 0 {
		avg = float64(weighted) / float64(wallMs)
	}
	return peak, avg
}

// PhaseConcurrency is the reconstructed parallelism of ONE phase (a canonical
// operation name) across a batch of runs, derived from the owner-measured
// StartedAt/FinishedAt anchors of the phase's timed operations.
type PhaseConcurrency struct {
	Count              int     `json:"count"`
	PeakConcurrency    int     `json:"peak_concurrency"`
	AverageConcurrency float64 `json:"average_concurrency"`
	WorkMs             int64   `json:"work_ms"`
	WallMs             int64   `json:"wall_ms"`
}

// derivePhaseConcurrency reconstructs per-phase concurrency from the timed
// operations of a batch of runs. Only operations with owner-measured
// StartedAt/FinishedAt anchors contribute intervals; owner-reported facts
// recorded without anchors (serialized values, e.g. a subprocess's own
// timings) are not concurrency intervals and stay out of the reconstruction.
// Phases are keyed by the canonical operation name (render, upload, ...).
func derivePhaseConcurrency(reports []RunReport) map[string]PhaseConcurrency {
	points := map[string][]concurrencyPoint{}
	work := map[string]int64{}
	count := map[string]int{}
	minStart := map[string]time.Time{}
	maxFinish := map[string]time.Time{}
	for _, r := range reports {
		for _, op := range r.Operations {
			if op.StartedAt.IsZero() || op.FinishedAt.IsZero() || op.FinishedAt.Before(op.StartedAt) {
				continue
			}
			points[op.Operation] = append(points[op.Operation],
				concurrencyPoint{at: op.StartedAt, delta: 1},
				concurrencyPoint{at: op.FinishedAt, delta: -1})
			work[op.Operation] += op.FinishedAt.Sub(op.StartedAt).Milliseconds()
			count[op.Operation]++
			if t, ok := minStart[op.Operation]; !ok || op.StartedAt.Before(t) {
				minStart[op.Operation] = op.StartedAt
			}
			if op.FinishedAt.After(maxFinish[op.Operation]) {
				maxFinish[op.Operation] = op.FinishedAt
			}
		}
	}
	if len(points) == 0 {
		return nil
	}
	out := make(map[string]PhaseConcurrency, len(points))
	for phase, pts := range points {
		wall := maxFinish[phase].Sub(minStart[phase]).Milliseconds()
		peak, avg := sweepConcurrency(pts, wall)
		out[phase] = PhaseConcurrency{
			Count:              count[phase],
			PeakConcurrency:    peak,
			AverageConcurrency: avg,
			WorkMs:             work[phase],
			WallMs:             wall,
		}
	}
	return out
}
