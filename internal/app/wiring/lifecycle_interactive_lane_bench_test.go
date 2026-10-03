package wiring

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
)

// These benchmarks are the executable proof of A1 (priority lane per tipo nel
// pool worker). They run on the REAL jobs-plane migrations and the REAL
// ClaimNext SQL — no fixtures, no fakes — so the numbers below are the same
// store work production does per claim.
//
// Measured production shape being reproduced (job-timing-20260928T152930Z):
// one script.generate sat 12,926s (3.6h) in the queue behind a stream of
// batch claims. The lane removes exactly that queue-position wait: the
// interactive family is claimed by its own workers on the FIRST claim.

const (
	// a1BacklogJobs is the batch backlog size for the scale test: larger than
	// any single worker's head-of-line window observed in the snapshot.
	a1BacklogJobs = 2000
	// a1BacklogBenchJobs keeps per-iteration seeding cheap enough for stable
	// benchmark numbers while still proving the ordering effect.
	a1BacklogBenchJobs = 50
	// a1InteractiveFirstClaimBudget is the acceptance budget: with the lane,
	// the first interactive claim must land in milliseconds regardless of the
	// batch backlog (pre-lane it was gated on the backlog, measured in hours).
	a1InteractiveFirstClaimBudget = 100 * time.Millisecond
)

// TestA1_ScaleBacklogInteractiveFirstClaimUnderBudget is the scale proof: with
// a2BacklogJobs batch jobs QUEUED ahead of it, the interactive job is claimed
// on the lane's FIRST claim, inside the budget. The pre-lane equivalent is
// reported (claims-before-interactive = the whole backlog) so the delta is
// visible in the test log, not just asserted.
func TestA1_ScaleBacklogInteractiveFirstClaimUnderBudget(t *testing.T) {
	db := newJobsPlaneTestDB(t)
	seedBenchmarkQueue(t, db, a1BacklogJobs)
	store := sqlitejobs.NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	start := time.Now()
	claimed, err := store.ClaimNext(ctx, "interactive-worker", time.Minute, interactiveJobTypes())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("interactive claim: %v", err)
	}
	if claimed == nil || claimed.ID != "interactive-1" {
		t.Fatalf("interactive claim = %+v, want interactive-1 on the FIRST claim behind %d batch jobs", claimed, a1BacklogJobs)
	}
	if elapsed > a1InteractiveFirstClaimBudget {
		t.Fatalf("first interactive claim took %s, budget %s", elapsed, a1InteractiveFirstClaimBudget)
	}
	t.Logf("lane: first interactive claim in %s behind %d batch jobs (pre-lane: %d claims ahead = the measured 3.6h head-of-line wait)",
		elapsed, a1BacklogJobs, a1BacklogJobs)

	// Sanity: the general complement still sees the whole backlog (the lane
	// starves nothing — it only reorders WHO claims the interactive family).
	general, err := store.ClaimNext(ctx, "general-worker", time.Minute, generalPoolJobTypes())
	if err != nil {
		t.Fatalf("general claim: %v", err)
	}
	if general == nil || general.Type == scriptpkg.TypeGenerate {
		t.Fatalf("general claim = %+v, want a batch job (the lane must not empty the backlog)", general)
	}
}

// BenchmarkA1_LaneFirstClaimBacklog50 measures the interactive lane's first
// claim with a 50-job batch backlog: this is the number that replaces the
// pre-lane head-of-line wait for every user-waiting generation job.
func BenchmarkA1_LaneFirstClaimBacklog50(b *testing.B) {
	ctx := context.Background()
	b.ReportMetric(a1BacklogBenchJobs, "backlog-size")
	b.ReportMetric(1, "interactive-claim-position")
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		db := newJobsPlaneTestDB(b)
		seedBenchmarkQueue(b, db, a1BacklogBenchJobs)
		store := sqlitejobs.NewSQLiteStore(db, zap.NewNop())
		b.StartTimer()

		claimed, err := store.ClaimNext(ctx, "interactive-worker", time.Minute, interactiveJobTypes())
		if err != nil {
			b.Fatalf("interactive claim: %v", err)
		}
		if claimed == nil || claimed.ID != "interactive-1" {
			b.Fatalf("interactive claim = %+v, want interactive-1", claimed)
		}
	}
}

// BenchmarkA1_PreLaneFirstClaimBacklog50 is the control: the SAME queue
// claimed WITHOUT the lane filter (pre-lane single pool). The interactive job
// surfaces only after every batch job ahead of it has been claimed — one
// ClaimNext at a time — which is the store-level cost of the measured 3.6h
// production head-of-line wait. The claims-before-interactive metric reports
// the backlog size (50) directly.
func BenchmarkA1_PreLaneFirstClaimBacklog50(b *testing.B) {
	ctx := context.Background()
	var claimsBefore int64
	b.ReportMetric(a1BacklogBenchJobs, "backlog-size")
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		db := newJobsPlaneTestDB(b)
		seedBenchmarkQueue(b, db, a1BacklogBenchJobs)
		store := sqlitejobs.NewSQLiteStore(db, zap.NewNop())
		b.StartTimer()

		var claimed *job.Job
		for {
			got, err := store.ClaimNext(ctx, "single-pool-worker", time.Minute, nil)
			if err != nil {
				b.Fatalf("claim: %v", err)
			}
			if got == nil {
				b.Fatalf("queue drained before the interactive job surfaced")
			}
			claimsBefore++
			if got.Type == scriptpkg.TypeGenerate {
				claimed = got
				break
			}
		}
		if claimed == nil || claimed.ID != "interactive-1" {
			b.Fatalf("pre-lane surfaced %+v, want interactive-1 last", claimed)
		}
	}
	b.ReportMetric(float64(claimsBefore)/float64(b.N), "interactive-claim-position")
}

// BenchmarkA1_GeneralChurnComplementFilter is the no-regression control for
// the lane: the general pool now claims with the complement filter (a long
// positive type list) instead of unfiltered. The benchmark churns
// create+claim pairs through the real store to show the filter's cost on the
// hot path is negligible.
func BenchmarkA1_GeneralChurnComplementFilter(b *testing.B) {
	benchmarkChurn(b, generalPoolJobTypes())
}

// BenchmarkA1_GeneralChurnUnfiltered is the baseline for the previous
// benchmark: the pre-lane unfiltered claim. Compare the two ns/op numbers to
// see the exact cost of the lane's complement filter.
func BenchmarkA1_GeneralChurnUnfiltered(b *testing.B) {
	benchmarkChurn(b, nil)
}

func benchmarkChurn(b *testing.B, types []string) {
	db := newJobsPlaneTestDB(b)
	store := sqlitejobs.NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	// Warm the queue so every iteration claims an existing job rather than
	// racing its own create. Batch-only: the churn measures the general path.
	for i := 0; i < 8; i++ {
		seedBatchJobsOnly(b, db, 1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		seedBatchJobsOnly(b, db, 1)
		b.StartTimer()

		claimed, err := store.ClaimNext(ctx, "churn-worker", time.Minute, types)
		if err != nil {
			b.Fatalf("claim: %v", err)
		}
		if claimed == nil {
			b.Fatalf("empty queue at iteration %d", i)
		}
		if len(types) > 0 && claimed.Type == scriptpkg.TypeGenerate {
			b.Fatalf("filtered claim took the interactive job %q", claimed.ID)
		}
	}
}
