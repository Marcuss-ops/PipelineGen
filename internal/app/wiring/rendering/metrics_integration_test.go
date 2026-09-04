package rendering

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

const chrononMetricsWiringSchema = `
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
`

const chrononMetricsWiringSidecar = `{
  "exclusive_wall_timeline": {
    "process_wall_ms": 32222.128358,
    "startup_ms": 5077.674858,
    "input_open_ms": 0.0,
    "prepare_ms": 2620.100047,
    "render_loop_ms": 24971.442112,
    "encoder_drain_finalize_ms": 554.31399,
    "ffprobe_ms": 374.566563,
    "sha256_ms": 0.0
  },
  "job": {
    "gpu": {
      "effective_backend": "vulkan",
      "decoder_backend": "nvdec",
      "encoder_backend": "nvenc",
      "gpu_upload_bytes": 2048,
      "gpu_readback_bytes": 4096
    }
  },
  "cache": {
    "gpu_asset_cache_hits": 0,
    "gpu_asset_cache_misses": 2
  }
}`

func TestNewChrononMetricsAdapterNilDBIsNil(t *testing.T) {
	if got := NewChrononMetricsAdapter(nil, zap.NewNop()); got != nil {
		t.Fatalf("NewChrononMetricsAdapter(nil DB) = non-nil, want nil")
	}
}

func TestNewChrononMetricsAdapterPersistsDuringARun(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(chrononMetricsWiringSchema); err != nil {
		t.Fatal(err)
	}

	adapter := NewChrononMetricsAdapter(db, zap.NewNop())
	if adapter == nil {
		t.Fatal("NewChrononMetricsAdapter over a real DB returned nil")
	}

	doc, err := cliprender.ParseChrononSidecar([]byte(chrononMetricsWiringSidecar))
	if err != nil {
		t.Fatal(err)
	}
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{
		JobID:     "job-clip-wiring",
		AttemptID: "attempt-1",
	})
	ctx := kernobs.WithRun(context.Background(), run)

	adapter.Publish(ctx, doc, cliprender.ChrononMetricsPublishOptions{
		SourceDurationMS: 45000,
		Width:            1920,
		Height:           1080,
		FPS:              30,
	})

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM performance_operations WHERE job_id = 'job-clip-wiring'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 7 {
		t.Fatalf("persisted %d rows, want 7", count)
	}
	var startupMS int64
	if err := db.QueryRow(`SELECT elapsed_ms FROM performance_operations
		WHERE operation = ? AND job_id = 'job-clip-wiring'`, cliprender.ChrononOperationStartup).Scan(&startupMS); err != nil {
		t.Fatal(err)
	}
	if startupMS != 5078 {
		t.Fatalf("chronon.startup elapsed_ms = %d, want 5078", startupMS)
	}
}
