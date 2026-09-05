package wiring

// chrononMetricsWiringSchema is the canonical performance_operations DDL used
// by wiring-level analytics tests. It mirrors the fixture in the rendering
// leaf package (same schema, same contract).
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

// chrononMetricsWiringSidecar is the canonical chronon metrics sidecar
// fixture parsed by wiring-level analytics tests (mirror of the rendering
// leaf package).
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
