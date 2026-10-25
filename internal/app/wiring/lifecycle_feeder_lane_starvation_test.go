package wiring

// Behavioural anti-starvation proof for the feeder lane (T7), mirroring the
// A1 interactive proof (lifecycle_interactive_lane_starvation_test.go): real
// migrations/sqlite_jobs schema, real ClaimNext SQL — no fixtures, no fakes.
//
// The measured failure this pins out of existence: asset.text.materialize
// queued 13.26x its own work (44.3h queue / 3.3h work) behind a batch
// backlog. Claim-order aging cannot help when every general worker is
// occupied; only dedicated workers guarantee the feeder family (and the
// generation consuming its output) reaches execution.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"go.uber.org/zap"

	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// seedFeederBacklog queues n general-pool batch jobs AHEAD of one job per
// feeder type, exactly the measured shape: batch work sitting between the
// feeders and their workers. Payloads go to the canonical job_payloads table
// (the jobs row is never widened — migration 004).
func seedFeederBacklog(t testing.TB, db *sql.DB, batchCount int) {
	t.Helper()
	seedBatchJobsOnly(t, db, batchCount)
	ctx := context.Background()
	now := time.Now().UTC()
	// Staggered created_at so the FIFO tiebreak is deterministic (same-
	// timestamp rows have no defined claim order).
	for i, seed := range []struct{ id, jobType string }{
		{"feeder-extract-1", "youtube_clip.extract"},
		{"feeder-materialize-1", "asset.text.materialize"},
	} {
		created := timeutil.FormatRFC3339(now.Add(time.Duration(i) * time.Second))
		if _, err := db.ExecContext(ctx,
			`INSERT INTO jobs (id, type, status, priority, created_at, updated_at, revision)
			 VALUES (?, ?, 'QUEUED', 5, ?, ?, 1)`, seed.id, seed.jobType, created, created); err != nil {
			t.Fatalf("seed feeder job %s: %v", seed.id, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO job_payloads (job_id, codec_id, payload, payload_hash, created_at)
			 VALUES (?, 'json', '{}', '', ?)`, seed.id, created); err != nil {
			t.Fatalf("seed feeder payload %s: %v", seed.id, err)
		}
	}
}

// TestFeederLaneNeverStarvesBehindBatchQueue is the T7 proof: with 50 batch
// jobs AHEAD of the feeder jobs in the same queue, the general claimer (the
// complement filter — exactly what buildJobRunner wires) can chew the backlog
// forever without ever touching a feeder job, while the feeder lane reaches
// its family on its FIRST claim.
func TestFeederLaneNeverStarvesBehindBatchQueue(t *testing.T) {
	db := newJobsPlaneTestDB(t)
	seedFeederBacklog(t, db, 50)

	store := sqlitejobs.NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	// The GENERAL pool claims batch work: 5 sequential claims must each take
	// a batch job, never a feeder one.
	generalTypes := generalPoolJobTypes()
	if len(generalTypes) == 0 {
		t.Fatal("general complement must be non-empty for this proof")
	}
	for i := 0; i < 5; i++ {
		claimed, err := store.ClaimNext(ctx, "general-worker", time.Minute, generalTypes)
		if err != nil {
			t.Fatalf("general claim %d: %v", i, err)
		}
		if claimed == nil {
			t.Fatalf("general claim %d: batch backlog unexpectedly empty", i)
		}
		for _, feederType := range feederJobTypes() {
			if claimed.Type == feederType {
				t.Fatalf("general claim %d took the feeder job %q (%s) — the lane leaked", i, claimed.ID, claimed.Type)
			}
		}
	}

	// The FEEDER lane claims its family directly: each type on its FIRST
	// claim, no wait, despite 45+ batch jobs still queued ahead.
	for _, wantID := range []string{"feeder-extract-1", "feeder-materialize-1"} {
		claimed, err := store.ClaimNext(ctx, "feeder-worker", time.Minute, feederJobTypes())
		if err != nil {
			t.Fatalf("feeder claim %s: %v", wantID, err)
		}
		if claimed == nil || claimed.ID != wantID {
			t.Fatalf("feeder claim = %+v, want %s on the FIRST claim", claimed, wantID)
		}
	}
}
