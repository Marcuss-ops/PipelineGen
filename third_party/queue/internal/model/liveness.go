package model

import (
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
)

// This file owns ONE thing: how a worker's heartbeat age becomes a liveness
// verdict. It is the single authority for that mapping.
//
// Why it is not in the repositories: the same question (\"is this worker alive?\")
// has exactly one answer, and two storage backends answering it independently
// (a Go loop in the in-memory store, a FILTER count in PostgreSQL) is how the
// old `/workers/health` came to disagree with `/workers`. The repositories are
// storage; the classification lives here and is applied by the service to the
// rows the store returns.

// WorkerLiveness is the derived liveness of a worker. It is an ALIAS of the
// public wire type, so the value an operator reads on GET /workers is the same
// declaration this package classifies with.
type WorkerLiveness = client.WorkerLiveness

const (
	WorkerLivenessReady    = client.WorkerLivenessReady
	WorkerLivenessDegraded = client.WorkerLivenessDegraded
	WorkerLivenessStale    = client.WorkerLivenessStale
	WorkerLivenessDead     = client.WorkerLivenessDead
)

// AllWorkerLivenesses re-exports the canonical liveness vocabulary.
func AllWorkerLivenesses() []WorkerLiveness { return client.AllWorkerLivenesses() }

// WorkerLivenessBoundaries is the resolved set of heartbeat-age boundaries for
// one classification pass. Resolving it ONCE per pass (rather than re-deriving
// the fractions per worker) is what keeps every worker in a snapshot judged
// against the same clock.
type WorkerLivenessBoundaries struct {
	// Valid reports whether a staleness window was configured. A window that
	// was never set (0) cannot classify anything as alive: see
	// ClassifyWorkerLiveness.
	Valid bool
	// DegradedAfter is the age beyond which a worker is DEGRADED:
	// staleAfter/3. With the queue unit's `-worker-stale-after 3m` and the
	// worker's 20s heartbeat that is 60s, so a worker three beats behind is
	// already visible as not-ready.
	DegradedAfter time.Duration
	// StaleBoundary is the age beyond which a worker is STALE:
	// 2*staleAfter/3 (120s for the shipped 3m window).
	StaleBoundary time.Duration
	// Window is the staleness window itself: beyond it the worker is DEAD.
	Window time.Duration
}

// WorkerLivenessWindow resolves the boundaries for a staleness window.
//
// A non-positive window is NOT treated as \"no restriction\" (which would call a
// worker that stopped days ago perfectly alive): it makes every worker dead,
// which is the honest answer when the queue cannot say how old a heartbeat may
// be before it stops counting. Fail closed, loudly, at the classification.
func WorkerLivenessWindow(staleAfter time.Duration) WorkerLivenessBoundaries {
	if staleAfter <= 0 {
		return WorkerLivenessBoundaries{}
	}
	degraded := staleAfter / 3
	return WorkerLivenessBoundaries{
		Valid:         true,
		DegradedAfter: degraded,
		// 2/3 computed as the remainder of the same division, so the two
		// boundaries can never overlap or invert for an odd window.
		StaleBoundary: staleAfter - degraded,
		Window:        staleAfter,
	}
}

// ClassifyWorkerLiveness returns the liveness of a worker whose last heartbeat
// was lastHeartbeat.
//
// The rule is one sentence: a worker that has sent nothing for a third of the
// staleness window is no longer \"ready\", for two thirds it is \"stale\", and
// beyond the window — or with no heartbeat at all — it is \"dead\".
//
// A zero lastHeartbeat is dead, never ready: a worker row is created by
// Register, which also records the first heartbeat, so a zero value means the
// timestamp was never read, and \"unknown\" must not be advertised as \"alive\".
// Clock skew (a heartbeat slightly in the future) is not an error state; such a
// worker classifies as ready.
func ClassifyWorkerLiveness(b WorkerLivenessBoundaries, now time.Time, lastHeartbeat time.Time) WorkerLiveness {
	if !b.Valid || lastHeartbeat.IsZero() {
		return WorkerLivenessDead
	}
	age := now.Sub(lastHeartbeat)
	switch {
	case age <= b.DegradedAfter:
		return WorkerLivenessReady
	case age <= b.StaleBoundary:
		return WorkerLivenessDegraded
	case age <= b.Window:
		return WorkerLivenessStale
	default:
		return WorkerLivenessDead
	}
}

// WithLiveness returns the worker annotated with its derived liveness. It is the
// projection GET /workers serves, so no reader has to re-derive it (and cannot
// derive it differently).
//
// A free function rather than a method because Worker is an ALIAS of the wire
// type (client.Worker): Go does not allow methods on a non-local type, and
// re-declaring the struct here to gain one would be exactly the second
// declaration this package exists to avoid.
func WithLiveness(w Worker, b WorkerLivenessBoundaries, now time.Time) Worker {
	w.Liveness = ClassifyWorkerLiveness(b, now, w.LastHeartbeatAt)
	return w
}

// SummarizeWorkerHealth aggregates a worker snapshot into the health report.
//
// It is a pure function of the rows the store returned, so the aggregate and the
// per-worker liveness an operator sees on GET /workers are computed from the
// same rows with the same boundaries — they cannot tell different stories about
// the same worker.
func SummarizeWorkerHealth(workers []Worker, b WorkerLivenessBoundaries, now time.Time) WorkerHealth {
	var h WorkerHealth
	for _, w := range workers {
		h.Total++
		switch ClassifyWorkerLiveness(b, now, w.LastHeartbeatAt) {
		case WorkerLivenessReady:
			// Only a live worker's REPORTED status is capacity: a frozen
			// worker that last said \"ready\" is not a ready worker.
			switch w.Status {
			case WorkerStatusReady:
				h.Ready++
			case WorkerStatusBusy:
				h.Busy++
			}
		case WorkerLivenessDegraded:
			h.Degraded++
		case WorkerLivenessStale:
			h.Stale++
		case WorkerLivenessDead:
			h.Offline++
		}
	}
	return h
}

// ReadyWorkerIDs returns the ids of the workers that are READY at now, in the
// order they appear in the snapshot.
//
// It is the primitive a preflight needs: \"can this deployment actually run a
// job right now?\" is answered by \"is at least one worker ready?\", not by \"is a
// worker row present?\".
func ReadyWorkerIDs(workers []Worker, b WorkerLivenessBoundaries, now time.Time) []string {
	var ids []string
	for _, w := range workers {
		if ClassifyWorkerLiveness(b, now, w.LastHeartbeatAt) == WorkerLivenessReady {
			ids = append(ids, w.ID)
		}
	}
	return ids
}
