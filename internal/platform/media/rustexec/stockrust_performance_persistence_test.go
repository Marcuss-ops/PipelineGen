package rustexec

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
	_ "github.com/mattn/go-sqlite3"
)

// stockrustRunMetadata is the canonical metadata_json shape persisted for a
// StockRust render benchmark so historical runs can be compared on rtf and
// ffmpeg_ms (wall_ms lives in its own dedicated column).
type stockrustRunMetadata struct {
	RTF             float64 `json:"rtf"`
	StockRenderMS   int64   `json:"stock_render_ms"`
	RustProcessMS   int64   `json:"rust_process_ms"`
	FFmpegMS        int64   `json:"ffmpeg_ms"`
	GoOverheadMS    int64   `json:"go_overhead_ms"`
	RustInternalMS  int64   `json:"rust_internal_ms"`
	MediaDurationMS int64   `json:"media_duration_ms"`
	InputBytes      int64   `json:"input_bytes"`
	OutputBytes     int64   `json:"output_bytes"`
}

// buildStockrustRun maps the measured three-wall breakdown into a durable
// performance Run row: wall_ms is the stock.render wall, and rtf / ffmpeg_ms /
// the full breakdown are embedded in metadata_json.
func buildStockrustRun(runID string, wall time.Duration, meta stockrustRunMetadata, startedAt, completedAt time.Time) capperformance.Run {
	return capperformance.Run{
		RunID:           runID,
		WorkloadID:      "stockrust_render",
		WorkloadVersion: "v1",
		Status:          "SUCCEEDED",
		WallMS:          wall.Milliseconds(),
		MetadataJSON:    mustJSON(meta),
		StartedAt:       startedAt.UTC().Format(time.RFC3339Nano),
		CompletedAt:     completedAt.UTC().Format(time.RFC3339Nano),
	}
}

// persistStockrustRun records the run into performance_runs when the operator
// points STOCKRUST_PERF_DB_PATH at a migrated SQLite database. Unset env keeps
// the benchmark hermetic (record-only in the test log).
func persistStockrustRun(t *testing.T, run capperformance.Run) {
	t.Helper()
	dbPath := os.Getenv("STOCKRUST_PERF_DB_PATH")
	if dbPath == "" {
		t.Logf("performance run %s NOT persisted (set STOCKRUST_PERF_DB_PATH to record into performance_runs)", run.RunID)
		return
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open STOCKRUST_PERF_DB_PATH %q: %v", dbPath, err)
	}
	defer db.Close()
	reg, err := perfstore.New(db)
	if err != nil {
		t.Fatalf("performance registry for %q: %v", dbPath, err)
	}
	if err := reg.RecordRun(context.Background(), run); err != nil {
		t.Fatalf("persist stockrust performance run %q: %v", run.RunID, err)
	}
	t.Logf("persisted performance run %s (wall_ms=%d) to %s", run.RunID, run.WallMS, dbPath)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// TestStockRustPerformancePersistenceRoundTrip proves that a measured StockRust
// run (wall_ms + rtf + ffmpeg_ms in metadata_json) survives a RecordRun into
// the performance_runs registry and reads back unchanged.
func TestStockRustPerformancePersistenceRoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE performance_runs (run_id TEXT PRIMARY KEY, job_id TEXT NOT NULL DEFAULT '', root_job_id TEXT NOT NULL DEFAULT '', video_id TEXT NOT NULL DEFAULT '', workload_id TEXT NOT NULL DEFAULT '', workload_version TEXT NOT NULL DEFAULT '', git_sha TEXT NOT NULL DEFAULT '', worker_id TEXT NOT NULL DEFAULT '', host_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL CHECK (status IN ('RUNNING','SUCCEEDED','FAILED')), wall_ms INTEGER NOT NULL DEFAULT 0, cpu_user_ms INTEGER NOT NULL DEFAULT 0, cpu_system_ms INTEGER NOT NULL DEFAULT 0, peak_rss_bytes INTEGER NOT NULL DEFAULT 0, disk_read_bytes INTEGER NOT NULL DEFAULT 0, disk_write_bytes INTEGER NOT NULL DEFAULT 0, network_rx_bytes INTEGER NOT NULL DEFAULT 0, network_tx_bytes INTEGER NOT NULL DEFAULT 0, metadata_json TEXT NOT NULL DEFAULT '{}', started_at TEXT NOT NULL, completed_at TEXT)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := perfstore.New(db)
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now().UTC().Truncate(time.Millisecond)
	completed := started.Add(10 * time.Second)
	meta := stockrustRunMetadata{
		RTF: 0.149, StockRenderMS: 10408, RustProcessMS: 10378, FFmpegMS: 10338,
		GoOverheadMS: 30, RustInternalMS: 40, MediaDurationMS: 70000,
		InputBytes: 1011610, OutputBytes: 1216857,
	}
	run := buildStockrustRun("stockrust-perf-roundtrip", 10*time.Second+408*time.Millisecond, meta, started, completed)
	if run.WallMS != 10408 {
		t.Fatalf("wall_ms = %d, want 10408", run.WallMS)
	}

	if err := reg.RecordRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	// Idempotent: re-recording the same run_id converges, not duplicates.
	if err := reg.RecordRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	var wallMS int64
	var metadataJSON string
	if err := db.QueryRow(`SELECT wall_ms, metadata_json FROM performance_runs WHERE run_id = ?`, run.RunID).Scan(&wallMS, &metadataJSON); err != nil {
		t.Fatal(err)
	}
	if wallMS != run.WallMS {
		t.Fatalf("persisted wall_ms = %d, want %d", wallMS, run.WallMS)
	}
	var got stockrustRunMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &got); err != nil {
		t.Fatalf("decode persisted metadata_json: %v", err)
	}
	if got.RTF != meta.RTF || got.FFmpegMS != meta.FFmpegMS || got.StockRenderMS != meta.StockRenderMS {
		t.Fatalf("metadata_json round-trip drift: got %+v want %+v", got, meta)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM performance_runs WHERE run_id = ?`, run.RunID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("run rows = %d, want 1 (idempotent upsert)", count)
	}
}
