package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// newJobsPlaneTestDB opens a temp-file jobs DB with the REAL
// migrations/sqlite_jobs applied (scope "jobs"), mirroring the e2e pattern of
// capabilities/jobs/deferral_e2e_test.go. The lane proof must run against the
// production claim SQL, not a fixture schema.
func newJobsPlaneTestDB(t testing.TB) *sql.DB {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations", "sqlite_jobs"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("jobs migrations dir not found at %s: %v (run from module root)", dir, err)
	}
	dbPath := filepath.Join(t.TempDir(), "jobs", "jobs.db.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir jobs plane: %v", err)
	}
	if err := storage.RunMigrationsOnDB(dbPath, zap.NewNop(), dir, "jobs"); err != nil {
		t.Fatalf("apply migrations/sqlite_jobs: %v", err)
	}
	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open migrated jobs DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedBenchmarkQueue fills the SQLite store with batch QUEUED jobs ahead of
// the interactive one, mirroring the measured production shape: a long stream
// of asset.text.materialize / youtube_clip.extract work sitting between a
// user-waiting script.generate and its worker. Payloads go to the canonical
// job_payloads table (the jobs row is never widened — migration 004).
func seedBenchmarkQueue(t testing.TB, db *sql.DB, batchCount int) {
	t.Helper()
	seedBatchJobsOnly(t, db, batchCount)
	now := timeutil.FormatRFC3339(time.Now().UTC())
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO jobs (id, type, status, priority, created_at, updated_at, revision)
		 VALUES ('interactive-1', ?, 'QUEUED', 5, ?, ?, 1)`,
		scriptpkg.TypeGenerate, now, now); err != nil {
		t.Fatalf("seed interactive job: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO job_payloads (job_id, codec_id, payload, payload_hash, created_at)
		 VALUES ('interactive-1', 'json', '{}', '', ?)`, now); err != nil {
		t.Fatalf("seed interactive payload: %v", err)
	}
}

// a1SeedSeq is the unique-id source for seeded jobs: benchmark iterations
// reseed the same store repeatedly, so ids must never collide.
var a1SeedSeq atomic.Int64

// seedBatchJobsOnly queues n batch jobs (asset.text.materialize, the dominant
// batch type in the timing snapshot) WITHOUT the interactive job — used by
// the churn benchmarks, which measure the general claim path alone.
func seedBatchJobsOnly(t testing.TB, db *sql.DB, n int) {
	t.Helper()
	ctx := context.Background()
	now := timeutil.FormatRFC3339(time.Now().UTC())
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("batch-%d", a1SeedSeq.Add(1))
		if _, err := db.ExecContext(ctx,
			`INSERT INTO jobs (id, type, status, priority, created_at, updated_at, revision)
			 VALUES (?, 'asset.text.materialize', 'QUEUED', 5, ?, ?, 1)`, id, now, now); err != nil {
			t.Fatalf("seed batch job %d: %v", i, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO job_payloads (job_id, codec_id, payload, payload_hash, created_at)
			 VALUES (?, 'json', '{}', '', ?)`, id, now); err != nil {
			t.Fatalf("seed batch payload %d: %v", i, err)
		}
	}
}

// TestA1_InteractiveLaneNeverStarvesBehindBatchQueue is the benchmark-shaped
// behavioural proof of the priority lane: with 50 batch jobs AHEAD of the
// interactive job in the same queue, the interactive claimer (the lane's type
// filter) reaches its job on its FIRST claim, while the general claimer (the
// complement filter) can chew the batch backlog for as long as it wants
// without ever touching the interactive job. Pre-lane, the interactive job
// waited behind the whole backlog (measured: 3.6h).
func TestA1_InteractiveLaneNeverStarvesBehindBatchQueue(t *testing.T) {
	db := newJobsPlaneTestDB(t)
	seedBenchmarkQueue(t, db, 50)

	store := sqlitejobs.NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	// The GENERAL pool (complement filter — exactly what buildJobRunner wires
	// when the lane is enabled) claims batch work. Run 5 sequential claims:
	// every one must take a batch job, never the interactive one.
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
		if claimed.Type == scriptpkg.TypeGenerate || claimed.Type == scriptpkg.TypeGenerateItem {
			t.Fatalf("general claim %d took the interactive job %q — the lane leaked", i, claimed.ID)
		}
	}

	// The INTERACTIVE lane claims its family directly: first claim, no wait.
	interactiveClaim, err := store.ClaimNext(ctx, "interactive-worker", time.Minute, interactiveJobTypes())
	if err != nil {
		t.Fatalf("interactive claim: %v", err)
	}
	if interactiveClaim == nil || interactiveClaim.ID != "interactive-1" {
		t.Fatalf("interactive claim = %+v, want interactive-1 on the FIRST claim", interactiveClaim)
	}

	// Registry-level sanity: the lane types the wiring uses are the ones the
	// dispatcher knows (the liveness invariant).
	reg := appjobs.Compose()
	for _, jt := range interactiveJobTypes() {
		if !reg.IsRegistered(jt) {
			t.Fatalf("lane type %q unregistered — lane would be dark in production", jt)
		}
	}
}
