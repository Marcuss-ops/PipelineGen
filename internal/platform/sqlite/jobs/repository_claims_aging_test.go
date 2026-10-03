package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// seedAgedQueuedJob inserts one QUEUED job row with an explicit created_at so
// a test can place a job arbitrarily far in the queue past. The timestamp is
// written in the canonical RFC3339 UTC format CreateJob uses, which is also
// what the aging expression's julianday() parse expects.
func seedAgedQueuedJob(t *testing.T, db *sql.DB, id, jobType string, priority int, createdAgo time.Duration, payload string) {
	t.Helper()
	created := timeutil.FormatRFC3339(time.Now().UTC().Add(-createdAgo))
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO jobs (id, type, status, priority, payload_json, created_at, updated_at, revision)
		 VALUES (?, ?, 'QUEUED', ?, ?, ?, ?, 1)`,
		id, jobType, priority, payload, created, created); err != nil {
		t.Fatalf("seed aged QUEUED job %s: %v", id, err)
	}
}

// TestClaimNextAgingOvertakesHigherPriorityAfterQueueHours pins the
// anti-starvation contract: a low-priority job that has queued long enough
// accumulates effective priority and overtakes a fresh higher-priority job.
// This is the measured production starvation (asset.text.materialize waited
// 13.26x its own work; one script.generate sat 3.6h behind a priority stream).
func TestClaimNextAgingOvertakesHigherPriorityAfterQueueHours(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	// Fresh priority-10 job: aging 0, effective 10.
	seedAgedQueuedJob(t, db, "job-fresh-high", "script.generate", 10, 0, "{}")
	// priority-1 job queued 12h ago: aging 12, effective 13 > 10.
	seedAgedQueuedJob(t, db, "job-old-low", "asset.text.materialize", 1, 12*time.Hour, "{}")

	claimed, err := store.ClaimNext(ctx, "worker-aging", time.Minute, nil)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claimed == nil || claimed.ID != "job-old-low" {
		t.Fatalf("ClaimNext = %+v, want job-old-low (12h of aging must overtake fresh priority-10)", claimed)
	}
}

// TestClaimNextAgingWithinFirstHourKeepsStrictPriority pins the compatibility
// half: inside the first queued hour the aging term is 0, so the historical
// strict (priority DESC, created_at ASC) ordering is unchanged for every job
// a worker sees in a normal batch window.
func TestClaimNextAgingWithinFirstHourKeepsStrictPriority(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	seedAgedQueuedJob(t, db, "job-fresh-high", "script.generate", 10, 0, "{}")
	// 59 minutes: aging term still 0 (CAST truncates), so priority wins.
	seedAgedQueuedJob(t, db, "job-old-low", "asset.text.materialize", 1, 59*time.Minute, "{}")

	claimed, err := store.ClaimNext(ctx, "worker-strict", time.Minute, nil)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claimed == nil || claimed.ID != "job-fresh-high" {
		t.Fatalf("ClaimNext = %+v, want job-fresh-high (aging must stay 0 within the first hour)", claimed)
	}
}

// TestClaimNextFIFOWithinSamePriorityAndAge pins that equal effective priority
// still falls back to created_at ASC (FIFO), never to insertion randomness.
func TestClaimNextFIFOWithinSamePriorityAndAge(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	seedAgedQueuedJob(t, db, "job-first", "clip.render", 5, 2*time.Hour, "{}")
	seedAgedQueuedJob(t, db, "job-second", "clip.render", 5, 1*time.Hour, "{}")

	claimed, err := store.ClaimNext(ctx, "worker-fifo", time.Minute, nil)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claimed == nil || claimed.ID != "job-first" {
		t.Fatalf("ClaimNext = %+v, want job-first (same priority: older wins)", claimed)
	}
}

// TestPeekQueuedMatchesClaimNextOrder pins the future-reader mirror: the
// preparation lookahead must see the job the claim path will actually pick
// next, including the aging term. A mismatch here makes preparation warm the
// wrong job.
func TestPeekQueuedMatchesClaimNextOrder(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	seedAgedQueuedJob(t, db, "job-fresh-high", "script.generate", 10, 0, "{}")
	seedAgedQueuedJob(t, db, "job-old-low", "asset.text.materialize", 1, 12*time.Hour, "{}")

	peeked, err := store.PeekQueued(ctx, 10)
	if err != nil {
		t.Fatalf("PeekQueued: %v", err)
	}
	if len(peeked) != 2 {
		t.Fatalf("PeekQueued returned %d jobs, want 2", len(peeked))
	}
	if peeked[0].ID != "job-old-low" {
		t.Fatalf("PeekQueued[0] = %s, want job-old-low (must mirror the aging claim order)", peeked[0].ID)
	}

	// And the real claim takes the same head of the queue.
	claimed, err := store.ClaimNext(ctx, "worker-mirror", time.Minute, nil)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claimed == nil || claimed.ID != peeked[0].ID {
		t.Fatalf("ClaimNext = %+v, want the PeekQueued head %s", claimed, peeked[0].ID)
	}
}

// TestClaimNextScopedAgingOvertakesHigherPriority pins that the payload-scoped
// claim shares the same anti-starvation ordering, so a dedicated pool cannot
// reintroduce the starvation the unscoped path fixed.
func TestClaimNextScopedAgingOvertakesHigherPriority(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	seedAgedQueuedJob(t, db, "job-scoped-fresh", "clip.render", 10, 0, `{"render_phase":"submit"}`)
	seedAgedQueuedJob(t, db, "job-scoped-old", "clip.render", 1, 12*time.Hour, `{"render_phase":"submit"}`)

	claimed, err := store.ClaimNextMatching(ctx, "worker-scoped", time.Minute, []string{"clip.render"},
		job.PayloadMatch{"render_phase": "submit"})
	if err != nil {
		t.Fatalf("ClaimNextMatching: %v", err)
	}
	if claimed == nil || claimed.ID != "job-scoped-old" {
		t.Fatalf("ClaimNextMatching = %+v, want job-scoped-old (scoped claim must age too)", claimed)
	}
}
