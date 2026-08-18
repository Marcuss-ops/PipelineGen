package config

type GoogleAccountingConfig struct {
	Enabled       bool   `yaml:"enabled" env:"VELOX_GOOGLE_ACCOUNTING_ENABLED" default:"false"`
	ServerURL     string `yaml:"server_url" env:"VELOX_GOOGLE_ACCOUNTING_URL" default:""`
	DownloadDir   string `yaml:"download_dir" env:"VELOX_GOOGLE_ACCOUNTING_DOWNLOAD_DIR" default:"./data/google_vids"`
	VidsProjectID string `yaml:"vids_project_id" env:"VELOX_GOOGLE_ACCOUNTING_VIDS_PROJECT_ID" default:""`
}

// QdrantConfig holds Qdrant vector-database connection parameters.
// QDRANT-003 (June 2026): canonical Qdrant configuration.
type QdrantConfig struct {
	// BaseURL is the Qdrant REST API base URL (e.g. "http://127.0.0.1:6333").
	BaseURL string `yaml:"base_url" env:"VELOX_QDRANT_URL" default:"http://127.0.0.1:6333"`
	// Timeout is the HTTP client timeout in seconds.
	Timeout int `yaml:"timeout" env:"VELOX_QDRANT_TIMEOUT" default:"10"`
	// Enabled controls whether Qdrant capability is active.
	// Default false: operators must explicitly opt in.
	Enabled bool `yaml:"enabled" env:"VELOX_QDRANT_ENABLED" default:"false"`
	// APIKey is an optional Qdrant API key for authenticated deployments.
	// When set, the Client sends X-Api-Key on every request.
	// QDRANT-005 Phase 1 (June 2026): propagated to both the
	// composition-root client and the admin reindex/clean-locators commands.
	APIKey string `yaml:"api_key" env:"VELOX_QDRANT_API_KEY" default:""`
	// ProjectionRetention is the number of known-good projection collections
	// to retain after an alias switch (active target + N-1 rollback targets).
	// It drives the automatic post-switch retention sweep via the canonical
	// capabilities/projectionretention policy. 0 disables the sweep; the
	// hard floor in the policy lifts anything below 2 back to 2. Default 2
	// keeps the active target plus one rollback.
	ProjectionRetention int `yaml:"projection_retention" env:"QDRANT_PROJECTION_RETENTION" default:"2"`
}

// OutboxConfig tunes the outbox_events worker pool. Defaults follow the
// CPU-only tuning from the PR-2 design review plus the July 2026
// stock-acquisition tuning: 500ms poll, batch 10, 4 workers (raised
// from 2 after the boxer stock runs observed a ~75-event pending
// backlog with only 2 processing), 5min per-entry timeout, 60s reclaim
// cadence, 360s stale threshold (2× process timeout), 5 max attempts.
// Env vars take precedence.
type OutboxConfig struct {
	PollIntervalMs         int `yaml:"poll_interval_ms" env:"VELOX_OUTBOX_POLL_MS" default:"500"`
	BatchSize              int `yaml:"batch_size" env:"VELOX_OUTBOX_BATCH_SIZE" default:"10"`
	Workers                int `yaml:"workers" env:"VELOX_OUTBOX_WORKERS" default:"4"`
	ProcessTimeoutSeconds  int `yaml:"process_timeout_seconds" env:"VELOX_OUTBOX_PROCESS_TIMEOUT_S" default:"300"`
	ReclaimIntervalSeconds int `yaml:"reclaim_interval_seconds" env:"VELOX_OUTBOX_RECLAIM_INTERVAL_S" default:"60"`
	StaleThresholdSeconds  int `yaml:"stale_threshold_seconds" env:"VELOX_OUTBOX_STALE_THRESHOLD_S" default:"360"`
	MaxAttempts            int `yaml:"max_attempts" env:"VELOX_OUTBOX_MAX_ATTEMPTS" default:"5"`
}

type LoggingConfig struct {
	Level     string `yaml:"level" env:"VELOX_LOG_LEVEL" default:"info"`
	Format    string `yaml:"format" env:"VELOX_LOG_FORMAT" default:"json"`
	ForceSync bool   `yaml:"force_sync" env:"VELOX_LOG_FORCE_SYNC" default:"false"`
}

// ServerConfig holds server-specific configuration.
// Port 8000 is the canonical default (chosen in the Operational Readiness
// PR — June 2026 — to free up port 8080 for unrelated services and to
// sidestep the historical conflict with SearXNG on 8080). Override
// via VELOX_PORT at runtime for per-deployment needs. The matching default
type ServerConfig struct {
	Host         string `yaml:"host" env:"VELOX_HOST" default:"127.0.0.1"`
	Port         int    `yaml:"port" env:"VELOX_PORT" default:"8000"`
	ReadTimeout  int    `yaml:"read_timeout" env:"VELOX_READ_TIMEOUT" default:"600"`
	WriteTimeout int    `yaml:"write_timeout" env:"VELOX_WRITE_TIMEOUT" default:"600"`
	GinMode      string `yaml:"gin_mode" env:"GIN_MODE" default:"release"`
}

// StorageConfig holds storage configuration.
// DataDir is the root for all data. MediaDir (relative to DataDir) is the
// single root for ALL media files on disk. Per-service subdirectories
// (voiceovers, images, youtube, etc.) are computed under MediaDir.
//
// The 6 explicit DB/dir fields below drive the canonical
// `internal/infrastructure/database.DatabaseSet` opened at boot
// (`codex/db-set-and-paths`). Defaults preserve the legacy single-file
// path PrimaryDBPath = <DataDir>/media/media.db.sqlite so existing
// deployments keep working without a migration. The path migration
// tool (`cmd/admin/path_migrate.go`, future PR) performs backup +
// SHA256 checksum + PRAGMA integrity_check + rollback when operators
type PathsConfig struct {
	CredentialsFile  string `yaml:"credentials_file" env:"VELOX_CREDENTIALS_FILE" default:"credentials.json"`
	TokenFile        string `yaml:"token_file" env:"VELOX_TOKEN_FILE" default:"token.json"`
	ClipTextDir      string `yaml:"clip_text_dir" env:"VELOX_CLIP_TEXT_DIR" default:""`
	PythonScriptsDir string `yaml:"python_scripts_dir" env:"VELOX_PYTHON_SCRIPTS_DIR" default:"scripts"`
	WorkflowsDir     string `yaml:"workflows_dir" env:"VELOX_WORKFLOWS_DIR" default:"./workflows"`
	// ArgosPythonBin is the interpreter that hosts the Argos Translate
	// sidecar (PR-ARGOS-TRANSLATION, Aug 2026). Defaults to the argostranslate
	// venv when set; empty falls back to the PATH-resolved python3. This is
	// separate from python_scripts_dir because Argos lives in its own venv
	// (.venv-argos) distinct from the Whisper runtime (.venv-whisper).
	ArgosPythonBin string `yaml:"argos_python_bin" env:"VELOX_ARGOS_PYTHON" default:""`
}

type WorkersConfig struct {
	AllowedIPs              []string `yaml:"allowed_ips" default:"[]"`
	HeartbeatTimeout        int      `yaml:"heartbeat_timeout" default:"30"`
	WorkerFailWindowSeconds int      `yaml:"worker_fail_window_seconds" default:"300"`
	WorkerFailThreshold     int      `yaml:"worker_fail_threshold" default:"3"`
}

// FeaturesConfig controls optional modules.
type FeaturesConfig struct {
	ArtlistEnabled bool `yaml:"artlist_enabled" env:"VELOX_FEATURE_ARTLIST_ENABLED" default:"false"`
	YouTubeEnabled bool `yaml:"youtube_enabled" env:"VELOX_FEATURE_YOUTUBE_ENABLED" default:"false"`
	// ClipRenderEnabled gates POST /api/clips/render + the clip.render
	// job binding (canonical VeloxEditing-compatible clip
	// post-processing capability). Default false: operators opt in
	// explicitly, mirroring the other feature flags.
	ClipRenderEnabled    bool `yaml:"clip_render_enabled" env:"VELOX_FEATURE_CLIP_RENDER_ENABLED" default:"false"`
	DriveEnabled         bool `yaml:"drive_enabled" env:"VELOX_FEATURE_DRIVE_ENABLED" default:"false"`
	ScriptClipsEnabled   bool `yaml:"script_clips_enabled" env:"VELOX_FEATURE_SCRIPT_CLIPS_ENABLED" default:"false"`
	VoiceoverEnabled     bool `yaml:"voiceover_enabled" env:"VELOX_FEATURE_VOICEOVER_ENABLED" default:"false"`
	ImagesEnabled        bool `yaml:"images_enabled" env:"VELOX_FEATURE_IMAGES_ENABLED" default:"false"`
	StockPipelineEnabled bool `yaml:"stock_pipeline_enabled" env:"VELOX_FEATURE_STOCK_PIPELINE_ENABLED" default:"true"`
	// CatalogScriptVectorSearch is Deprecated (PR-LEGACY-CLEANUP-2026-07-10 Item 3, 2026-07-10;
	// see architecture/action-plans/2026-07-10-legacy-cleanup-5-item-orchestration.md §3).
	// The corresponding top-level `vector_search:` yaml block is removed; the canonical
	// Qdrant config block (`qdrant:` with `base_url:`) supersedes it. The field is
	// RETAINED for backward compat at the wire layer; the read site at
	// `internal/app/search_backend_semantic.go` (as of 2026-07-10) is a no-op gate
	// pending `PR-CATALOG-SCRIPT-VECTOR-SEARCH-RETIRE` (deadline 2026-08-15) which
	// migrates call sites to the canonical Qdrant path.
	// Safe operational choice: leave the env var at its default `false`.
	CatalogScriptVectorSearch bool `yaml:"catalog_script_vector_search" env:"VELOX_FEATURE_CATALOG_SCRIPT_VECTOR_SEARCH" default:"false"`

	// MediaDriveRequired, when true, causes asset registration to fail
	// when Drive upload is not successful (PUBLISH_FAILED or LOCAL_ONLY).
	// Assets are never marked as "registered locally" with partial-success
	// semantics. The failure surfaces as ErrYouTubeDriveRequired sentinel.
	//
	// Default false (backward-compatible): Drive upload is best-effort.
	// Set to true for production deployments where every clip MUST
	// have a Drive file (Step 4 of YouTube Clips Deploy Readiness,
	// July 2026).
	MediaDriveRequired bool `yaml:"media_drive_required" env:"VELOX_MEDIA_DRIVE_REQUIRED" default:"false"`
}

// ToDatabaseStorageConfig projects this StorageConfig into the
// storage.StorageConfig consumed by `internal/infrastructure/database.OpenSet`.
