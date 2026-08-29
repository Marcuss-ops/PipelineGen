package cliprender

// chronon_metrics_persistence_integration_test.go proves the canonical
// persistence path end-to-end with real components (godlike/07 no-fake-
// availability: real SQL round-trips, no mocks):
//
//	run bound to ctx (exactly what the job worker does with
//	StartRunForClaim + WithRun)
//	    → ChrononMetricsAdapter.Publish (the execution layer)
//	    → perfstore.OperationStore (the OperationReportProjectionRecorder
//	      seam — the only writer of performance_operations)
//	    → performance_operations rows in SQLite
//
// This is the guarantee behind "every job run saves the Chronon metrics to
// SQLite automatically": the same components the clip.render worker uses,
// driven through the same run context.

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
	"go.uber.org/zap"
)

// chrononPerformanceOperationsSchema is the canonical performance_operations
// DDL (migration 217) — the table the adapter's rows land in.
const chrononPerformanceOperationsSchema = `
CREATE TABLE IF NOT EXISTS performance_operations (
    operation_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    step_id TEXT NOT NULL,
    operation TEXT NOT NULL,
    source_sha256 TEXT NOT NULL DEFAULT '',
    source_duration_ms INTEGER NOT NULL DEFAULT 0,
    source_size_bytes INTEGER NOT NULL DEFAULT 0,
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    fps REAL NOT NULL DEFAULT 0,
    input_codec TEXT NOT NULL DEFAULT '',
    output_codec TEXT NOT NULL DEFAULT '',
    elapsed_ms INTEGER NOT NULL DEFAULT 0,
    cpu_user_ms INTEGER NOT NULL DEFAULT 0,
    cpu_system_ms INTEGER NOT NULL DEFAULT 0,
    output_size_bytes INTEGER NOT NULL DEFAULT 0,
    cache_hit INTEGER NOT NULL DEFAULT 0,
    strategy TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_performance_operations_operation ON performance_operations(operation, created_at);
CREATE INDEX IF NOT EXISTS idx_performance_operations_run ON performance_operations(run_id, created_at);
`

// TestChrononMetricsAdapterPersistsToSQLiteDuringARun drives the full
// canonical path: a run bound to ctx, the adapter publishing the sidecar
// phases, and the real OperationStore writing one row per measured phase.
func TestChrononMetricsAdapterPersistsToSQLiteDuringARun(t *testing.T) {
	db := openChrononMetricsDB(t)
	store, err := perfstore.NewOperationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewChrononMetricsAdapter(store, zap.NewNop())

	// A real run bound to ctx, exactly like the job worker's execution path
	// (StartRunForClaim + WithRun). The store resolves run_id/job_id from
	// this run — never from an invented identity.
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{
		JobID:     "job-clip-1",
		AttemptID: "attempt-1",
	})
	ctx := kernobs.WithRun(context.Background(), run)

	sc, err := ParseChrononSidecar([]byte(chrononSidecarFixture))
	if err != nil {
		t.Fatal(err)
	}
	daemonReused := true
	adapter.Publish(ctx, sc, ChrononMetricsPublishOptions{
		DaemonReused:     &daemonReused,
		SourceSHA256:     "sha-source-1",
		SourceDurationMS: 45000,
		OutputSizeBytes:  2_500_000,
		Width:            1920,
		Height:           1080,
		FPS:              30,
	})

	wantOps := map[string]int64{
		ChrononOperationStartup:      5078,
		ChrononOperationInputOpen:    0,
		ChrononOperationPrepare:      2620,
		ChrononOperationRenderLoop:   24971,
		ChrononOperationEncoderDrain: 554,
		ChrononOperationFFprobe:      375,
		ChrononOperationSHA256:       0,
	}
	rows, err := db.Query(`SELECT run_id, job_id, operation, elapsed_ms, source_sha256,
		source_duration_ms, output_size_bytes, width, height, fps, metadata_json
		FROM performance_operations ORDER BY operation`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var (
			runID, jobID, operation, sourceSHA, meta string
			elapsed, sourceDur, outputBytes          int64
			width, height                            int
			fps                                      float64
		)
		if err := rows.Scan(&runID, &jobID, &operation, &elapsed, &sourceSHA,
			&sourceDur, &outputBytes, &width, &height, &fps, &meta); err != nil {
			t.Fatal(err)
		}
		count++
		wantElapsed, ok := wantOps[operation]
		if !ok {
			t.Fatalf("unexpected persisted operation %q", operation)
		}
		if elapsed != wantElapsed {
			t.Fatalf("%s elapsed_ms = %d, want %d", operation, elapsed, wantElapsed)
		}
		if runID != run.Report().RunID {
			t.Fatalf("%s run_id = %q, want canonical run %q", operation, runID, run.Report().RunID)
		}
		if jobID != "job-clip-1" {
			t.Fatalf("%s job_id = %q, want job-clip-1", operation, jobID)
		}
		if sourceSHA != "sha-source-1" || sourceDur != 45000 || outputBytes != 2_500_000 ||
			width != 1920 || height != 1080 || fps != 30 {
			t.Fatalf("%s certified columns not persisted: sha=%q dur=%d out=%d %dx%d@%v",
				operation, sourceSHA, sourceDur, outputBytes, width, height, fps)
		}
		var metaDoc map[string]any
		if err := json.Unmarshal([]byte(meta), &metaDoc); err != nil {
			t.Fatalf("%s metadata_json is not valid JSON: %s", operation, meta)
		}
		if metaDoc["backend"] != "direct_yuv_cuda" || metaDoc["decoder"] != "nvdec" ||
			metaDoc["encoder"] != "nvenc" || metaDoc["daemon_reused"] != true {
			t.Fatalf("%s metadata_json missing attempt context: %s", operation, meta)
		}
		if metaDoc["cuda_upload_bytes"] != float64(4194304) || metaDoc["cuda_readback_bytes"] != float64(1048576) {
			t.Fatalf("%s metadata_json missing CUDA bytes: %s", operation, meta)
		}
		delete(wantOps, operation)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 7 {
		t.Fatalf("persisted %d rows, want 7", count)
	}
	if len(wantOps) != 0 {
		t.Fatalf("missing persisted phases: %v", wantOps)
	}
}

// TestChrononMetricsAdapterRejectsPublishOutsideARun pins the fail-closed
// contract of the canonical store: without a run bound to ctx, a publish is
// best-effort logged (never a render failure) and writes NOTHING — a
// performance row without a canonical run identity would be an orphaned
// second source of truth.
func TestChrononMetricsAdapterRejectsPublishOutsideARun(t *testing.T) {
	db := openChrononMetricsDB(t)
	store, err := perfstore.NewOperationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewChrononMetricsAdapter(store, zap.NewNop())

	sc, err := ParseChrononSidecar([]byte(chrononSidecarFixture))
	if err != nil {
		t.Fatal(err)
	}
	// No run bound to ctx — the render path in benchmarks/standalone use.
	adapter.Publish(context.Background(), sc, ChrononMetricsPublishOptions{})

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM performance_operations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("publish outside a run wrote %d rows, want 0", count)
	}
}

func openChrononMetricsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(chrononPerformanceOperationsSchema); err != nil {
		t.Fatal(err)
	}
	return db
}
