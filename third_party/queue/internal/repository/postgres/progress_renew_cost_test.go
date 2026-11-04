package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/migrate"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// progress_renew_cost_test.go measures the DB cost of the worker client's
// PERIODIC writes — the "DB metrics" that gate any client-side event batching
// (progress + renewal coalescing):
//
//	progress push : every 10s per in-flight job (progresspush.DefaultInterval)
//	              → SetProgress = 1 conditional UPDATE, NO event row
//	lease renewal : every lease/2 (5 min at the queue's 10m lease)
//	              → Renew = 1 tx {UPDATE render_jobs, SELECT attempt,
//	                              UPDATE render_attempts, INSERT LEASE_RENEWED}
//
// Verdict is recorded below from the measured numbers on a local PostgreSQL.
// TLDR: batching is NOT justified — the required load is a fraction of one
// percent of what one PostgreSQL connection already handles, and progress
// pushes write no events at all (pinned by TestSetProgressWritesNoEvents), so
// there is nothing on that path for "event batching" to batch.

// setupRepoB is setupRepo for benchmarks (the harness is identical; the
// package skips both unless TEST_DATABASE_URL is set).
func setupRepoB(b *testing.B, lease time.Duration, maxAttempts int) *Repository {
	b.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		b.Skip("TEST_DATABASE_URL not set; skipping postgres integration benchmark")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		b.Fatal(err)
	}
	if err := migrate.Apply(ctx, db); err != nil {
		b.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE render_jobs CASCADE`); err != nil {
		b.Fatal(err)
	}
	return New(db, lease, maxAttempts)
}

// claimedJob submits and claims one job so progress/renew have a live lease.
func claimedJob(b *testing.B, r *Repository, id, worker string) {
	b.Helper()
	ctx := context.Background()
	if err := r.Submit(ctx, model.Job{ID: id}); err != nil {
		b.Fatal(err)
	}
	if _, _, err := r.Claim(ctx, worker); err != nil {
		b.Fatal(err)
	}
}

// TestSetProgressWritesNoEvents pins the contract the batching question rests
// on: a progress push updates the job row and NOTHING else — it is not an
// event source. A regression that started appending progress events would
// turn the throttled push into render_events churn and change the batching
// calculus, so the premise is enforced, not assumed.
func TestSetProgressWritesNoEvents(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	ctx := context.Background()
	if err := r.Submit(ctx, model.Job{ID: "progress-no-events"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := r.SetProgress(ctx, "progress-no-events", "w1", model.Progress{FramesDone: i, TotalFrames: 100}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM render_events WHERE job_id = 'progress-no-events'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	// Exactly the claim event (JOB_CREATED + JOB_CLAIMED = 2); zero from the
	// five pushes.
	if n != 2 {
		t.Fatalf("render_events rows = %d after 5 pushes, want 2 (JOB_CREATED + JOB_CLAIMED only)", n)
	}
}

// BenchmarkSetProgress measures one throttled progress push.
//
// Verdict (2026-10-27, go test -bench, PostgreSQL 16 in the repo's compose
// stack on the reference machine):
//
//	BenchmarkSetProgress-24    642    3463740 ns/op
//	BenchmarkRenew-24          415    6096147 ns/op
//
// ~3.5ms per push — dominated by per-call round-trip/commit overhead in this
// environment, which is exactly what client-side batching would amortize.
// The load math still says no: a push runs every 10s PER IN-FLIGHT JOB
// (progresspush.DefaultInterval), so gpu_lanes × 6 pushes/min — three lanes
// ≈ 0.3 req/s ≈ 1% of ONE connection's capacity at the measured rate — and
// even 100 lanes would sit at ~10 req/s. Batching the progress path would
// trade a wire-contract change for nothing measurable below O(100) concurrent
// lanes per queue; re-measure here if a deployment ever reaches that scale.
func BenchmarkSetProgress(b *testing.B) {
	r := setupRepoB(b, 30*time.Second, 3)
	claimedJob(b, r, "bench-progress", "w1")
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.SetProgress(ctx, "bench-progress", "w1", model.Progress{FramesDone: i, TotalFrames: b.N}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRenew measures one lease renewal — the ONLY periodic worker call
// that appends an event row (LEASE_RENEWED).
//
// Verdict (2026-10-27, numbers in the BenchmarkSetProgress comment): ~6.1ms
// per renewal including the event INSERT, at one renewal per lease/2 = per 5
// minutes per job (12 event rows/hour/job). A 3-lane worker produces ~0.01
// renewals/s ≈ 0.07% of one connection at the measured rate. Batching
// renewals across jobs would require a wire change and break per-job lease
// atomicity for a load PostgreSQL does not notice.
func BenchmarkRenew(b *testing.B) {
	r := setupRepoB(b, 30*time.Second, 3)
	claimedJob(b, r, "bench-renew", "w1")
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.Renew(ctx, "bench-renew", "w1"); err != nil {
			b.Fatal(err)
		}
	}
}
