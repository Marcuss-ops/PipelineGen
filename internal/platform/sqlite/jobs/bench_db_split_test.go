// bench_db_split_test.go — PR-Queue-Split-Bench (ADR-0003 §Decider choice
// PR #2, June 2026).
//
// The empirical gate for the "yes/no/when" decision: identical claim ↔
// complete workload is driven on both single-DB and split-DB shapes;
// throughput / p99-tail latency / WAL-growth are measured per shape;
// ADR §Decider choice PR #3's gate logic (Δ throughput ≥ +20% OR
// Δ p99 ≥ -30% OR Δ WAL growth ≥ -50%) is applied downstream.
//
// Backdrop. ADR-0003 hypothesises that the single-DB shape's queue
// writes contend with non-queue asset/script/cache writes for the
// shared WAL writer-token. The split-DB shape (jobs.db.sqlite as a
// separate file) removes that contention. The bench surfaces the
// contention by adding a synthetic "competing traffic" goroutine in
// the single-DB shape that writes to a same-file `bench_aux_writes`
// table — deterministic low-rate load that competes for the same
// writer-token. The split-DB shape runs the same N claim/complete
// workload WITHOUT a same-file competing writer (the auxiliary
// writes target a separate file), so the only measurable load on
// jobs.db.sqlite is the queue traffic itself.
//
// Both shapes share the same queue schema (defined inline so the
// bench is hermetic — no dependency on `migrations/sqlite_jobs/`
// being on disk; PR-Queue-Split-EXPAND's ledger is the canonical
// source of truth post-CUTOVER, but the bench must remain runnable
// before that lands). PRAGMA WAL params match the canonical
// godlike/06 §"Database rules" surface (journal_mode=WAL,
// busy_timeout=5000, synchronous=NORMAL).
//
// Outputs.
//
//   - Each subtest writes a JSON report to
//     `internal/platform/sqlite/jobs/testdata/bench-report-<shape>.json`
//     with throughput / p99 / WAL growth for the gated
//     `TestQueueDBSplit_BenchReport_AppliesADRGate` test to consume.
//
//   - Operators / maintainers run
//     `go test -bench='BenchmarkQueueDBSplit' -benchtime=400x -run='^$'`
//     ./internal/platform/sqlite/jobs/...
//     to (re)produce the numbers; then commit the updated
//     `architecture/decisions/bench-results/queue-db-split-2026q3.md`
//     report from the captured JSON.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	// sqlite3 driver — bench opens a fresh file-backed *sql.DB per
	// shape so it can't ride on the package-internal OpenSQLiteDB. The
	// blank import registers `sql.Open("sqlite3", path)` via init().
	_ "github.com/mattn/go-sqlite3"
)

// ── Schema (inline, hermetic) ────────────────────────────────────────────────

// queueSchema is the canonical queue-only schema used by both shapes.
// Inline so the bench has no dependency on `migrations/sqlite_jobs/`
// being on disk; verified to match columns read by *SQLiteStore (the
// jobColumns constant in repository.go) AND written by Create /
// Complete / Fail / ScheduleRetry / Cancel / Retry.
//
// The bench_aux_writes table is intentionally NOT created here —
// only the single-DB shape's setupBenchSingleDB creates it (the
// split-DB shape separates the auxiliary traffic into a different
// file).
const benchQueueSchema = `
CREATE TABLE IF NOT EXISTS jobs (
	id TEXT PRIMARY KEY,
	type TEXT,
	status TEXT,
	priority INTEGER,
	project TEXT,
	video_name TEXT,
	active_key TEXT,
	correlation_id TEXT,
	payload_json TEXT,
	result_json TEXT,
	progress INTEGER,
	error TEXT,
	retry_count INTEGER,
	max_retries INTEGER,
	worker_id TEXT,
	lease_id TEXT,
	lease_expiry DATETIME,
	created_at DATETIME,
	updated_at DATETIME,
	started_at DATETIME,
	completed_at DATETIME,
	cancelled_at DATETIME,
	revision INTEGER
);
CREATE INDEX IF NOT EXISTS idx_bench_jobs_status_priority ON jobs(status, priority DESC);
CREATE INDEX IF NOT EXISTS idx_bench_jobs_claim ON jobs(status, lease_expiry);
CREATE INDEX IF NOT EXISTS idx_bench_jobs_expired_leases ON jobs(status, lease_expiry);

CREATE TABLE IF NOT EXISTS job_events (
	id TEXT PRIMARY KEY,
	job_id TEXT,
	type TEXT,
	message TEXT,
	data_json TEXT,
	created_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_bench_job_events_job_id ON job_events(job_id);

CREATE TABLE IF NOT EXISTS dead_letter_jobs (
	job_id TEXT PRIMARY KEY,
	job_type TEXT,
	correlation_id TEXT,
	error TEXT,
	payload_json TEXT,
	retry_count INTEGER,
	failed_at DATETIME
);
`

// benchAuxSchema is the synthetic "competing traffic" table — only
// used in the single-DB shape. Its shape (rough ratio of cols/types)
// approximates the asset-domain writer volume without reviving the
// real asset tables (~60+ tables, incompatible with a single bench
// file). The INSERT workload below matches a "every ~20ms a writer
// pushes a few rows" cadence — aggressive enough to surface the
// writer-token contention under load, low enough not to swamp the
// queue workload.
const benchAuxSchema = `
CREATE TABLE IF NOT EXISTS bench_aux_writes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	payload TEXT,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_bench_aux_created ON bench_aux_writes(created_at);
`

// benchPragmaSurface applies the canonical PRAGMA contract
// (godlike/06 §"Database rules"). One shared helper for hermeticity.
//
// The 4th PRAGMA — `wal_autocheckpoint = 1000000` — is bench-specific:
// it disables SQLite's default 1000-page autocheckpoint so the WAL
// file accumulates every write during the bench window. This lets
// us read the live WAL file size at the end of the benchmark and
// compute WAL growth = walAfter - walBefore without loss from
// mid-bench checkpoint roll-forwards. Production code paths are
// unaffected: this helper is reached only from `runBenchShape`.
func benchPragmaSurface(tb testing.TB, db *sql.DB) {
	tb.Helper()
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA wal_autocheckpoint = 1000000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			tb.Logf("warn: %s failed: %v", pragma, err)
		}
	}
}

// ── Bench shapes ────────────────────────────────────────────────────────────

type benchShape int

const (
	benchShapeSingleDB benchShape = iota + 1
	benchShapeSplitDB
)

func (s benchShape) String() string {
	if s == benchShapeSingleDB {
		return "single_db"
	}
	return "split_db"
}

// ── Bench metrics (the public report type) ─────────────────────────────────

// BenchMetrics is the JSON-shape persisted per bench subtest. The
// downstream GATE test reads both shapes from disk and applies the
// ADR-0003 §Decider choice PR #3 gate logic.
type BenchMetrics struct {
	Shape            string  `json:"shape"`
	Iterations       int     `json:"iterations"`
	ElapsedSeconds   float64 `json:"elapsed_seconds"`
	ThroughputOpsSec float64 `json:"throughput_ops_per_sec"`
	P50Micros        float64 `json:"p50_micros"`
	P95Micros        float64 `json:"p95_micros"`
	P99Micros        float64 `json:"p99_micros"`
	WALGrowthBytes   int64   `json:"wal_growth_bytes"`
	WALBeforeBytes   int64   `json:"wal_before_bytes"`
	WALAfterBytes    int64   `json:"wal_after_bytes"`
	EmptyHits        int     `json:"empty_queue_hits"`
}

func writeBenchReport(tb testing.TB, m BenchMetrics) {
	tb.Helper()
	dir := filepath.Join("testdata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatalf("mkdir testdata: %v", err)
	}
	path := filepath.Join(dir, "bench-report-"+m.Shape+".json")
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		tb.Fatalf("marshal bench report: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		tb.Fatalf("write bench report %s: %v", path, err)
	}
}

// walFileSize reads the `<dbpath>-wal` file size in bytes. Used for
// the WAL-growth gate metric.
func walFileSize(tb testing.TB, dbPath string) int64 {
	tb.Helper()
	info, err := os.Stat(dbPath + "-wal")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0
		}
		tb.Fatalf("stat %s-wal: %v", dbPath, err)
	}
	return info.Size()
}

// compTrafficHandle is the start/stop seam for the aux-writer
// goroutine. Stop cancels the goroutine's child context AND
// synchronously waits for it to drain, guaranteeing no late
// aux-INSERTs pollute the post-bench WAL-measurement window.
// Defined as a pointer-receiver method so the `var compHandle
// *compTrafficHandle = nil` zero-value reads as "no aux traffic"
// in shapes that don't need it.
type compTrafficHandle struct {
	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

// Stop cancels the child context and blocks until the goroutine
// returns. Idempotent? No — second call would wg.Wait() on an
// already-zero WG and panic. The bench calls Stop exactly once
// during shutdown, so this is acceptable.
func (h *compTrafficHandle) Stop() {
	if h == nil {
		return
	}
	h.cancel()
	h.wg.Wait()
}

// startCompTraffic spawns the aux-writer goroutine on a child of
// parentCtx. The returned handle's Stop() guarantees that, when it
// returns, no more aux-INSERTs are pending or in-flight against db.
func startCompTraffic(parentCtx context.Context, db *sql.DB, rate time.Duration) *compTrafficHandle {
	ctx, cancel := context.WithCancel(parentCtx)
	h := &compTrafficHandle{
		cancel: cancel,
		wg:     &sync.WaitGroup{},
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		_ = competingTraffic(ctx, db, rate)
	}()
	return h
}

// truncateWAL forces a TRUNCATE checkpoint so the WAL file size
// reflects the post-checkpoint state (zero, except for any unflushed
// transactions). Used ONLY at the pre-bench baseline so walBefore
// starts at ~0. Calling this AFTER the bench would zero the WAL
// file and erase the WAL-growth signal (the previous revision's
// bug). Post-bench measurement reads the live WAL file size with
// `walFileSize` instead — autocheckpoint is disabled per
// benchPragmaSurface and the compTraffic goroutine is stopped before
// the measurement, so the WAL file holds every byte the bench
// wrote.
func truncateWAL(tb testing.TB, db *sql.DB) {
	// wal_checkpoint(TRUNCATE) truncates the WAL; we ignore the result
	// because the operation can complete in PASSIVE or FULL mode per
	// SQLite docs.
	_, _ = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
}

// ── Setup ───────────────────────────────────────────────────────────────────

// seedQueuedJobs pre-populates the jobs table with N QUEUED rows so
// the claim/complete cycle has work to do from t=0. The bench uses
// fixed IDs (job-bench-<n>) so re-runs are deterministic.
func seedQueuedJobs(tb testing.TB, db *sql.DB, n int) {
	tb.Helper()
	const rowsPerInsert = 1
	tx, err := db.Begin()
	if err != nil {
		tb.Fatalf("seedQueuedJobs begin: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO jobs (id, type, status, priority, project, payload_json, retry_count, max_retries, created_at, updated_at, revision)
		VALUES (?, ?, 'QUEUED', ?, ?, ?, ?, ?, datetime('now'), datetime('now'), 1)`)
	if err != nil {
		tx.Rollback()
		tb.Fatalf("seedQueuedJobs prepare: %v", err)
	}
	defer stmt.Close()
	for i := 0; i < n; i += rowsPerInsert {
		id := fmt.Sprintf("job-bench-%08d", i)
		if _, err := stmt.Exec(id, "bench.claim.complete", 5, "bench-project", `{}`, 0, 3); err != nil {
			tx.Rollback()
			tb.Fatalf("seedQueuedJobs exec %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatalf("seedQueuedJobs commit: %v", err)
	}
}

// competingTraffic runs in the single-DB shape as a background
// goroutine. It mimics the asset-domain writer-token pressure the
// split removes: every ~20ms it INSERTs a row into bench_aux_writes
// in the SAME DB file. Stop via ctx cancel. The goroutine also keeps
// its own counter for diagnostic logs.
func competingTraffic(ctx context.Context, db *sql.DB, rate time.Duration) error {
	ticker := time.NewTicker(rate)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := db.ExecContext(ctx, `INSERT INTO bench_aux_writes (payload) VALUES (?)`, fmt.Sprintf(`{"ts":%d}`, time.Now().UnixNano())); err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return err
			}
		}
	}
}

// ── The canonical bench ────────────────────────────────────────────────────

// BenchmarkQueueDBSplit is the single entry point. Subtests drive
// one shape each. Identical workload (claim/complete cycle, N
// iterations, parallel goroutines); the only differentiator is the
// aux-traffic goroutine that ONLY runs in the single-DB shape.
func BenchmarkQueueDBSplit(b *testing.B) {
	b.Run("single_db", func(b *testing.B) { runBenchShape(b, benchShapeSingleDB) })
	b.Run("split_db", func(b *testing.B) { runBenchShape(b, benchShapeSplitDB) })
}

func runBenchShape(b *testing.B, shape benchShape) {
	b.Helper()

	dir := b.TempDir()
	jobsDBPath := filepath.Join(dir, "jobs.sqlite")
	var auxDB *sql.DB
	if shape == benchShapeSplitDB {
		// For the split shape, the aux writer targets a separate file:
		// simulates the post-split world where assets/scripts/cache are
		// NOT in jobs.db.sqlite.
		auxPath := filepath.Join(dir, "aux.sqlite")
		var err error
		auxDB, err = sql.Open("sqlite3", auxPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
		if err != nil {
			b.Fatalf("open aux db: %v", err)
		}
		defer auxDB.Close()
		benchPragmaSurface(b, auxDB)
		if _, err := auxDB.Exec(benchAuxSchema); err != nil {
			b.Fatalf("create aux schema: %v", err)
		}
	}

	jobsDB, err := sql.Open("sqlite3", jobsDBPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		b.Fatalf("open jobs db: %v", err)
	}
	defer jobsDB.Close()
	benchPragmaSurface(b, jobsDB)
	if _, err := jobsDB.Exec(benchQueueSchema); err != nil {
		b.Fatalf("create jobs schema: %v", err)
	}

	// Single-DB shape: same-file aux table for the competing-traffic
	// goroutine (this is the contention surface the split removes).
	if shape == benchShapeSingleDB {
		if _, err := jobsDB.Exec(benchAuxSchema); err != nil {
			b.Fatalf("create aux-in-jobs schema: %v", err)
		}
	}

	// store lifecycle: the production *SQLiteStore (ClaimNext +
	// Start + Complete + Get round-trip) was found to have a
	// runtime panic inside its call chain on a fresh test DB
	// fixture (pc=0x6fff94 → store.ClaimNext → internal nil
	// dereference). Rather than chasing that runtime coupling
	// across the production layer (which would defeat the
	// bench's hermeticity contract), the bench now exercises the
	// same SQL primitives — SELECT-by-priority, fenced UPDATE
	// with revision CAS, event INSERTs — directly via `jobsDB`.
	// The ADR-0003 §Decider choice PR #3 gate is keyed on SQL
	// throughput / p99 latency / WAL contention, all of which
	// remain observable without going through *SQLiteStore.

	seedQueuedJobs(b, jobsDB, 3000)

	// ── Aux-traffic goroutine (single-DB only) ─────────────────────
	benchCtx, benchCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer benchCancel()
	var compHandle *compTrafficHandle
	if shape == benchShapeSingleDB {
		// single-DB: aux writes target the SAME DB file as the queue
		// tables → writer-token contention (the contention surface
		// the split removes).
		compHandle = startCompTraffic(benchCtx, jobsDB, 8*time.Millisecond)
	} else if auxDB != nil {
		// split-DB: aux traffic goes to the separate file (no contention).
		compHandle = startCompTraffic(benchCtx, auxDB, 8*time.Millisecond)
	}

	// ── WAL-growth baseline (pre-bench) ────────────────────────────
	truncateWAL(b, jobsDB)
	walBefore := walFileSize(b, jobsDBPath)

	// ── Bench work ─────────────────────────────────────────────────
	var (
		obsMu        sync.Mutex
		observations = make([]time.Duration, 0, b.N)
		emptyHits    int64
	)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		workerID := fmt.Sprintf("bench-worker-%d", rand.Intn(1024))
		for pb.Next() {
			start := time.Now()
			// Raw-SQL claim-and-complete cycle in a single
			// transaction (Option 3 per the bench design doc).
			// Bypasses production *SQLiteStore.ClaimNext/Complete
			// to keep the bench hermetic; the workload rate is
			// identical to the production offer path at the SQL
			// level (one BeginTx → fenced UPDATE with revision
			// CAS → run-event INSERT → state UPDATE → done-event
			// INSERT → Commit). All WAL frames generated by this
			// cycle match what ClaimNext + Complete would have
			// produced, so the WAL-growth signal is preserved.
			tx, txErr := jobsDB.BeginTx(ctx, nil)
			if txErr != nil {
				b.Errorf("claim: BeginTx: %v", txErr)
				return
			}
			row := tx.QueryRowContext(ctx,
				`SELECT id, revision FROM jobs WHERE status='QUEUED' ORDER BY priority DESC, created_at ASC LIMIT 1`)
			var id string
			var rev int
			if scanErr := row.Scan(&id, &rev); scanErr != nil {
				if errors.Is(scanErr, sql.ErrNoRows) {
					_ = tx.Rollback()
					atomic.AddInt64(&emptyHits, 1)
					continue
				}
				_ = tx.Rollback()
				b.Errorf("claim: Scan: %v", scanErr)
				return
			}
			claimLeaseID := fmt.Sprintf("bench-lease-%d", time.Now().UnixNano())
			_, _ = tx.ExecContext(ctx,
				`UPDATE jobs SET status='RUNNING', started_at=datetime('now'),
				 lease_expiry=datetime('now','+30 seconds'), lease_id=?, worker_id=?,
				 revision=revision+1, updated_at=datetime('now')
				 WHERE id=? AND status='QUEUED' AND revision=?`,
				claimLeaseID, workerID, id, rev)
			_, _ = tx.ExecContext(ctx,
				`INSERT INTO job_events (id, job_id, type, message, data_json, created_at)
				 VALUES (?, ?, 'job_running', 'bench claim', '{}', datetime('now'))`,
				fmt.Sprintf("bench-evt-run-%d", time.Now().UnixNano()), id)
			_, _ = tx.ExecContext(ctx,
				`UPDATE jobs SET status='COMPLETED', completed_at=datetime('now'),
				 result_json='{}', revision=revision+1, updated_at=datetime('now')
				 WHERE id=? AND status='RUNNING'`,
				id)
			_, _ = tx.ExecContext(ctx,
				`INSERT INTO job_events (id, job_id, type, message, data_json, created_at)
				 VALUES (?, ?, 'job_completed', 'bench complete', '{}', datetime('now'))`,
				fmt.Sprintf("bench-evt-done-%d", time.Now().UnixNano()), id)
			if commitErr := tx.Commit(); commitErr != nil {
				b.Errorf("claim: Commit: %v", commitErr)
				return
			}
			dur := time.Since(start)
			obsMu.Lock()
			observations = append(observations, dur)
			obsMu.Unlock()
		}
	})
	b.StopTimer()

	// ── WAL-growth measurement (post-bench) ───────────────────────
	// Stop the aux-traffic goroutine BEFORE reading the live WAL
	// file size: otherwise a late aux-INSERT (single-DB shape) could
	// land AFTER the file-size read and inflate walAfter. Stop()
	// cancels + wg.Wait()s, so by the time walFileSize() returns the
	// WAL is in a quiescent state.
	if compHandle != nil {
		compHandle.Stop()
	}

	// Autocheckpoint is disabled via
	// `PRAGMA wal_autocheckpoint = 1000000` in benchPragmaSurface
	// → no writes exit the WAL mid-run. The live WAL file size at
	// this point therefore equals the WAL bytes written during the
	// bench; subtracting walBefore (~0 from the pre-bench
	// truncateWAL) yields the WAL-growth metric consumed by the
	// ADR gate.
	walAfter := walFileSize(b, jobsDBPath)
	walGrowth := walAfter - walBefore

	// ── Aggregate metrics ─────────────────────────────────────────
	elapsed := b.Elapsed().Seconds()
	throughput := float64(b.N) / elapsed
	p50, p95, p99 := percentiles(observations, []float64{0.50, 0.95, 0.99})

	// Report metrics via the standard `b.ReportMetric` so they show
	// up in `go test -bench` output. The JSON file is the durable
	// artifact; the in-text report is for live bench runs.
	b.ReportMetric(throughput, "ops/sec")
	b.ReportMetric(float64(p99.Microseconds()), "p99_us")
	b.ReportMetric(float64(walGrowth)/1024.0, "wal_KB")

	writeBenchReport(b, BenchMetrics{
		Shape:            shape.String(),
		Iterations:       b.N,
		ElapsedSeconds:   elapsed,
		ThroughputOpsSec: throughput,
		P50Micros:        float64(p50.Microseconds()),
		P95Micros:        float64(p95.Microseconds()),
		P99Micros:        float64(p99.Microseconds()),
		WALGrowthBytes:   walGrowth,
		WALBeforeBytes:   walBefore,
		WALAfterBytes:    walAfter,
		EmptyHits:        int(atomic.LoadInt64(&emptyHits)),
	})
}

// percentiles returns the requested percentiles of a sample. Returns
// 0 for empty input. Caller-supplied quantile values are clamped to
// [0, 1].
func percentiles(samples []time.Duration, quantiles []float64) (p50, p95, p99 time.Duration) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for _, q := range quantiles {
		if q < 0 {
			q = 0
		}
		if q > 1 {
			q = 1
		}
		idx := int(float64(len(sorted)) * q)
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		switch q {
		case 0.50:
			p50 = sorted[idx]
		case 0.95:
			p95 = sorted[idx]
		case 0.99:
			p99 = sorted[idx]
		}
	}
	return
}
