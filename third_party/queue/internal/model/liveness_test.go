package model

import (
	"testing"
	"time"
)

// TestWorkerLivenessWindowBoundaries pins the age boundaries the whole
// classification rests on, because they are the only place a fraction of the
// staleness window is turned into a decision.
func TestWorkerLivenessWindowBoundaries(t *testing.T) {
	b := WorkerLivenessWindow(90 * time.Second)
	if !b.Valid {
		t.Fatal("90s window must be a valid window")
	}
	if b.DegradedAfter != 30*time.Second {
		t.Fatalf("degraded after %s, want 30s", b.DegradedAfter)
	}
	if b.StaleBoundary != 60*time.Second {
		t.Fatalf("stale after %s, want 60s", b.StaleBoundary)
	}
	if b.Window != 90*time.Second {
		t.Fatalf("window %s, want 90s", b.Window)
	}

	// An odd window must not produce overlapping or inverted boundaries: the
	// two inner boundaries are the quotient and the remainder of one division.
	odd := WorkerLivenessWindow(10 * time.Second)
	if !(odd.DegradedAfter < odd.StaleBoundary && odd.StaleBoundary < odd.Window) {
		t.Fatalf("odd window produced unordered boundaries: %+v", odd)
	}

	// A window that was never configured cannot classify anything as alive.
	if WorkerLivenessWindow(0).Valid {
		t.Fatal("a zero window must not be reported as a valid window")
	}
	if WorkerLivenessWindow(-time.Second).Valid {
		t.Fatal("a negative window must not be reported as a valid window")
	}
}

// TestClassifyWorkerLiveness pins the age → verdict mapping at and around every
// boundary. The interesting cases are the exact boundaries: an off-by-one here
// is what makes one implementation call a worker ready while the other calls it
// dead.
func TestClassifyWorkerLiveness(t *testing.T) {
	b := WorkerLivenessWindow(90 * time.Second)
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		age  time.Duration
		want WorkerLiveness
	}{
		{"fresh", 0, WorkerLivenessReady},
		{"one beat", 20 * time.Second, WorkerLivenessReady},
		{"exactly one missed beat", 30 * time.Second, WorkerLivenessReady},
		{"just past one missed beat", 30*time.Second + time.Nanosecond, WorkerLivenessDegraded},
		{"two missed beats", 60 * time.Second, WorkerLivenessDegraded},
		{"just past two missed beats", 60*time.Second + time.Nanosecond, WorkerLivenessStale},
		{"at the window edge", 90 * time.Second, WorkerLivenessStale},
		{"past the window", 90*time.Second + time.Nanosecond, WorkerLivenessDead},
		{"frozen for hours", 4 * time.Hour, WorkerLivenessDead},
		// Clock skew: a heartbeat slightly in the future is not a fault, and
		// must never be classified as anything but alive.
		{"skewed into the future", -5 * time.Second, WorkerLivenessReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyWorkerLiveness(b, now, now.Add(-tc.age))
			if got != tc.want {
				t.Fatalf("age %s → %s, want %s", tc.age, got, tc.want)
			}
		})
	}
}

// TestClassifyWorkerLivenessFailsClosed pins the two ways a worker becomes dead
// without an age: no heartbeat was ever read, and no window was configured.
func TestClassifyWorkerLivenessFailsClosed(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	b := WorkerLivenessWindow(90 * time.Second)

	if got := ClassifyWorkerLiveness(b, now, time.Time{}); got != WorkerLivenessDead {
		t.Fatalf("a worker with no heartbeat classified %s, want %s: unknown must never read as alive", got, WorkerLivenessDead)
	}
	if got := ClassifyWorkerLiveness(WorkerLivenessWindow(0), now, now); got != WorkerLivenessDead {
		t.Fatalf("with no staleness window a fresh worker classified %s, want %s: a missing bound must not mean no bound", got, WorkerLivenessDead)
	}
}

// TestSummarizeWorkerHealthCountsLivenessNotReportedStatus is the acceptance
// criterion for the frozen-worker defect: a worker whose STORED status is ready
// but whose heartbeat has frozen must not be counted as ready capacity.
func TestSummarizeWorkerHealthCountsLivenessNotReportedStatus(t *testing.T) {
	b := WorkerLivenessWindow(90 * time.Second)
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)

	workers := []Worker{
		{ID: "live-ready", Status: WorkerStatusReady, LastHeartbeatAt: now.Add(-5 * time.Second)},
		{ID: "live-busy", Status: WorkerStatusBusy, LastHeartbeatAt: now.Add(-5 * time.Second)},
		{ID: "last-said-ready-but-frozen", Status: WorkerStatusReady, LastHeartbeatAt: now.Add(-10 * time.Minute)},
		{ID: "one-missed-beat", Status: WorkerStatusReady, LastHeartbeatAt: now.Add(-40 * time.Second)},
		{ID: "two-missed-beats", Status: WorkerStatusReady, LastHeartbeatAt: now.Add(-80 * time.Second)},
		{ID: "never-heartbeated", Status: WorkerStatusReady},
	}

	h := SummarizeWorkerHealth(workers, b, now)
	if h.Ready != 1 || h.Busy != 1 {
		t.Fatalf("ready/busy = %d/%d, want 1/1: only live workers are capacity", h.Ready, h.Busy)
	}
	if h.Degraded != 1 || h.Stale != 1 {
		t.Fatalf("degraded/stale = %d/%d, want 1/1", h.Degraded, h.Stale)
	}
	if h.Offline != 2 {
		t.Fatalf("offline = %d, want 2 (the frozen worker and the one that never heartbeated)", h.Offline)
	}
	if h.Total != len(workers) {
		t.Fatalf("total = %d, want %d", h.Total, len(workers))
	}
}

// TestSummarizeWorkerHealthPartitionIsExact pins that every worker whose
// liveness is not ready lands in exactly one non-ready bucket, so the aggregate
// can never lose a worker between the bands.
func TestSummarizeWorkerHealthPartitionIsExact(t *testing.T) {
	b := WorkerLivenessWindow(90 * time.Second)
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)

	var workers []Worker
	for i, age := range []time.Duration{
		0, 15 * time.Second, 45 * time.Second, 75 * time.Second, 2 * time.Hour,
	} {
		workers = append(workers, Worker{
			ID:              string(rune('a' + i)),
			Status:          WorkerStatusBusy,
			LastHeartbeatAt: now.Add(-age),
		})
	}
	h := SummarizeWorkerHealth(workers, b, now)
	if got := h.Ready + h.Busy + h.Degraded + h.Stale + h.Offline; got != h.Total {
		t.Fatalf("buckets sum to %d, want total %d: a worker fell between the bands", got, h.Total)
	}
}

// TestReadyWorkerIDs pins the preflight primitive: only ready workers, and in
// snapshot order.
func TestReadyWorkerIDs(t *testing.T) {
	b := WorkerLivenessWindow(90 * time.Second)
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	workers := []Worker{
		{ID: "w-dead", Status: WorkerStatusReady, LastHeartbeatAt: now.Add(-time.Hour)},
		{ID: "w-ready", Status: WorkerStatusBusy, LastHeartbeatAt: now.Add(-time.Second)},
		{ID: "w-degraded", Status: WorkerStatusReady, LastHeartbeatAt: now.Add(-31 * time.Second)},
	}
	ids := ReadyWorkerIDs(workers, b, now)
	if len(ids) != 1 || ids[0] != "w-ready" {
		t.Fatalf("ready workers = %v, want [w-ready]: a frozen or ageing worker is not a preflight pass", ids)
	}
}

// TestWorkerLivenessVocabularyIsTheWireVocabulary pins that the derived
// vocabulary has four states and that the alias re-export cannot drift from the
// wire declaration (an operator reading `liveness` on the wire must be reading
// a value this package can produce).
func TestWorkerLivenessVocabularyIsTheWireVocabulary(t *testing.T) {
	all := AllWorkerLivenesses()
	want := []WorkerLiveness{
		WorkerLivenessReady, WorkerLivenessDegraded,
		WorkerLivenessStale, WorkerLivenessDead,
	}
	if len(all) != len(want) {
		t.Fatalf("liveness vocabulary = %v, want %v", all, want)
	}
	for i := range want {
		if all[i] != want[i] {
			t.Fatalf("liveness vocabulary[%d] = %q, want %q", i, all[i], want[i])
		}
	}
}

// TestWorkerWithLivenessAnnotatesTheProjection pins the GET /workers projection:
// the annotation is derived from the heartbeat, not copied from the status.
func TestWorkerWithLivenessAnnotatesTheProjection(t *testing.T) {
	b := WorkerLivenessWindow(90 * time.Second)
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	w := Worker{ID: "w1", Status: WorkerStatusReady, LastHeartbeatAt: now.Add(-10 * time.Minute)}
	annotated := WithLiveness(w, b, now)
	if annotated.Liveness != WorkerLivenessDead {
		t.Fatalf("liveness = %q, want %q", annotated.Liveness, WorkerLivenessDead)
	}
	if annotated.Status != WorkerStatusReady {
		t.Fatalf("the reported status must survive the annotation, got %q", annotated.Status)
	}
}
