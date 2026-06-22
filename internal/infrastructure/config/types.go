// Package config provides configuration management for the PipelineGen system.
package config

import (
	"path/filepath"
)

// ConcurrencyConfig holds system-wide concurrency limits for resource-bound
// operations. Every limit was previously hardcoded as a package-level channel
// capacity or a const (values 1–3). For a 100-worker deployment these defaults
// have been raised to ≥10 so workers don't serialise behind a single bottleneck.
// Each field accepts a zero value (0) to disable the limit entirely, though
// disabling video extraction or script generation is rarely useful in practice.
type ConcurrencyConfig struct {
	// MaxConcurrentVideoExtracts limits parallel yt-dlp download + cut operations.
	// Was hardcoded at 1 (SQLite lock serialisation); WAL mode now allows ≥10.
	MaxConcurrentVideoExtracts int `yaml:"max_concurrent_video_extracts" env:"VELOX_CONCURRENT_VIDEO_EXTRACTS" default:"10"`

	// MaxConcurrentScriptGenerations limits concurrent LLM script generation.
	// Was hardcoded at 2; raised to 50 for 100-worker parallelism.
	MaxConcurrentScriptGenerations int `yaml:"max_concurrent_script_generations" env:"VELOX_CONCURRENT_SCRIPT_GENERATIONS" default:"50"`

	// MaxConcurrentNvidiaGenerations limits concurrent GPU image generation requests.
	// Was hardcoded at 2; raised to 10 (VRAM-bound).
	MaxConcurrentNvidiaGenerations int `yaml:"max_concurrent_nvidia_generations" env:"VELOX_CONCURRENT_NVIDIA_GENERATIONS" default:"10"`

	// MaxConcurrentOllamaCalls limits concurrent Ollama model invocations.
	// Was hardcoded at 2; raised to 50 (model server should handle this load).
	MaxConcurrentOllamaCalls int `yaml:"max_concurrent_ollama_calls" env:"VELOX_CONCURRENT_OLLAMA_CALLS" default:"50"`

	// MaxConcurrentChannelChecks limits concurrent YouTube channel monitor checks.
	// Was hardcoded at 3; raised to 20.
	MaxConcurrentChannelChecks int `yaml:"max_concurrent_channel_checks" env:"VELOX_CONCURRENT_CHANNEL_CHECKS" default:"20"`
}

// Config holds all configuration for the application.
// All fields are public and read-only after bootstrap. The previous
// sync.RWMutex was decorative (fields were mutated directly without locking)
// so it has been removed to avoid a false guarantee of thread safety.
type Config struct {
	Server           ServerConfig           `yaml:"server"`
	Logging          LoggingConfig          `yaml:"logging"`
	Storage          StorageConfig          `yaml:"storage"`
	Security         SecurityConfig         `yaml:"security"`
	External         ExternalConfig         `yaml:"external"`
	Paths            PathsConfig            `yaml:"paths"`
	Drive            DriveConfig            `yaml:"drive"`
	Concurrency      ConcurrencyConfig      `yaml:"concurrency"`
	Jobs             JobsConfig             `yaml:"jobs"`
	Workers          WorkersConfig          `yaml:"workers"`
	Video            VideoConfig            `yaml:"video"`
	Features         FeaturesConfig         `yaml:"features"`
	GoogleAccounting GoogleAccountingConfig `yaml:"google_accounting"`
	ClipIndexer      ClipIndexerConfig      `yaml:"clip_indexer"`
	VectorSearch     VectorSearchConfig     `yaml:"vector_search"`
	Reranker         RerankerConfig         `yaml:"reranker"`
	Books            BooksConfig            `yaml:"books"`
	VLM              VLMConfig              `yaml:"vlm"`
	Lessons          LessonsConfig          `yaml:"lessons"`
	Multilingual     MultilingualConfig     `yaml:"multilingual"`
	Scripts          ScriptsConfig          `yaml:"scripts"`
	Outbox           OutboxConfig           `yaml:"outbox"`
}

// GoogleAccountingConfig configures the Google Accounting sidecar used for
// AI-video flows. The sidecar is OFF by default; when Enabled=true the
// pipeline routes per-clip image and AI-avatar requests through it and
// stores downloads under DownloadDir (relative to the project root unless
// absolute).
type GoogleAccountingConfig struct {
	Enabled       bool   `yaml:"enabled" env:"VELOX_GOOGLE_ACCOUNTING_ENABLED" default:"false"`
	ServerURL     string `yaml:"server_url" env:"VELOX_GOOGLE_ACCOUNTING_URL" default:""`
	DownloadDir   string `yaml:"download_dir" env:"VELOX_GOOGLE_ACCOUNTING_DOWNLOAD_DIR" default:"./data/google_vids"`
	VidsProjectID string `yaml:"vids_project_id" env:"VELOX_GOOGLE_ACCOUNTING_VIDS_PROJECT_ID" default:""`
}

// OutboxConfig tunes the outbox_events worker pool. Defaults follow the
// CPU-only tuning from the PR-2 design review: 500ms poll, batch 10,
// 2 workers, 5min per-entry timeout, 60s reclaim cadence, 360s stale
// threshold (2× process timeout), 5 max attempts. Env vars take precedence
// over the yaml file.
type OutboxConfig struct {
	PollIntervalMs         int `yaml:"poll_interval_ms" env:"VELOX_OUTBOX_POLL_MS" default:"500"`
	BatchSize              int `yaml:"batch_size" env:"VELOX_OUTBOX_BATCH_SIZE" default:"10"`
	Workers                int `yaml:"workers" env:"VELOX_OUTBOX_WORKERS" default:"2"`
	ProcessTimeoutSeconds  int `yaml:"process_timeout_seconds" env:"VELOX_OUTBOX_PROCESS_TIMEOUT_S" default:"300"`
	ReclaimIntervalSeconds int `yaml:"reclaim_interval_seconds" env:"VELOX_OUTBOX_RECLAIM_INTERVAL_S" default:"60"`
	StaleThresholdSeconds  int `yaml:"stale_threshold_seconds" env:"VELOX_OUTBOX_STALE_THRESHOLD_S" default:"360"`
	MaxAttempts            int `yaml:"max_attempts" env:"VELOX_OUTBOX_MAX_ATTEMPTS" default:"5"`
}

// LoggingConfig holds logger-specific configuration.
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
// for clients is the VELOX_MASTER_URL env var (see WorkersConfig.MasterURL).
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
// opt in.
type StorageConfig struct {
	// DataDir is the root for ALL persisted data (DBs + blobs).
	DataDir string `yaml:"data_dir" env:"VELOX_DATA_DIR" default:"./data"`
	// PrimaryDBPath is the file path for the unified media DB
	// (jobs, assets, scripts, search_queries, worker_nodes, media_assets,
	//  clip_folders, voiceovers, etc.). Defaults preserve legacy
	// `<DataDir>/media/media.db.sqlite`.
	PrimaryDBPath string `yaml:"primary_db_path" env:"VELOX_PRIMARY_DB_PATH" default:""`
	// ObservabilityDBPath is the file path for the API request log DB
	// (`api_requests` table + indexes). Distinct from PrimaryDBPath so
	// log retention doesn't churn the schema-versioned primary DB.
	// Default: `<DataDir>/observability/api_requests.db.sqlite`.
	ObservabilityDBPath string `yaml:"observability_db_path" env:"VELOX_OBSERVABILITY_DB_PATH" default:""`
	// WorkspaceDir is for transient job scratch space.
	WorkspaceDir string `yaml:"workspace_dir" env:"VELOX_WORKSPACE_DIR" default:""`
	// CacheDir is for derived artifacts (re-rendered thumbnails, etc.).
	CacheDir string `yaml:"cache_dir" env:"VELOX_CACHE_DIR" default:""`
	// ExportDir is for one-off exports (download bundles, audit dumps).
	ExportDir string `yaml:"export_dir" env:"VELOX_EXPORT_DIR" default:""`
	// ObservabilityMaxAgeDays is the retention cutoff for the
	// observability DB (`admin db rotate`). Rows with ts older than
	// this are offloaded to <DataDir>/backups/observability-<DATE>.db.sqlite
	// then DELETEd from the live DB. 0 disables rotation. See
	// ARCHITECTURE.md §12 (observability retention policy).
	ObservabilityMaxAgeDays int `yaml:"observability_max_age_days" env:"VELOX_OBSERVABILITY_MAX_AGE_DAYS" default:"7"`
	// ObservabilityMaxSizeMB is the soft cap on the observability DB
	// size. After each rotation, `admin db status` reports the WAL
	// + main file size; if it exceeds this, an operator should run
	// `admin db rotate` with a smaller -max-age-days.
	ObservabilityMaxSizeMB int `yaml:"observability_max_size_mb" env:"VELOX_OBSERVABILITY_MAX_SIZE_MB" default:"1024"`
	// MediaDir / TempDir are kept for backward-compat with the legacy
	// on-disk filesystem layout (voiceovers, images, youtube, etc.).
	MediaDir string `yaml:"media_dir" env:"PIPELINEGEN_MEDIA_DIR" default:"media"`
	TempDir  string `yaml:"temp_dir" env:"VELOX_TEMP_DIR" default:"tmp"`
}

// MediaPath returns the full path to the unified media directory.
func (s StorageConfig) MediaPath() string { return s.FullPath(s.MediaDir) }

// FullPath returns the absolute path to a subdirectory within DataDir.
func (s StorageConfig) FullPath(subDir string) string {
	if filepath.IsAbs(subDir) {
		return subDir
	}
	return filepath.Join(s.DataDir, subDir)
}

// TempPath returns the full path to the temporary directory.
func (s StorageConfig) TempPath() string { return s.FullPath(s.TempDir) }

// mediaSubPath returns the full path to a subdirectory under MediaDir.
func (s StorageConfig) mediaSubPath(sub string) string {
	return filepath.Join(s.DataDir, s.MediaDir, sub)
}

// VoiceoversPath returns the full path to the voiceovers directory.
func (s StorageConfig) VoiceoversPath() string { return s.mediaSubPath("voiceovers") }

// AssetsPath returns the full path to the main assets directory.
func (s StorageConfig) AssetsPath() string { return s.mediaSubPath("assets") }

// DownloadsPath returns the full path to the downloads directory.
func (s StorageConfig) DownloadsPath() string { return s.mediaSubPath("downloads") }

// BackupsPath returns the full path to the backups directory.
func (s StorageConfig) BackupsPath() string { return s.FullPath("backups") }

// AnimationsPath returns the full path to the animations directory.
func (s StorageConfig) AnimationsPath() string { return s.mediaSubPath("animations") }

// YoutubeClipsPath returns the full path to the youtube clips directory.
func (s StorageConfig) YoutubeClipsPath() string { return s.mediaSubPath("youtube") }

// ArtlistPath returns the full path to the artlist directory.
func (s StorageConfig) ArtlistPath() string { return s.mediaSubPath("artlist") }

// ImagesPath returns the full path to the images directory.
func (s StorageConfig) ImagesPath() string { return s.mediaSubPath("images") }

// SecurityConfig holds security-related configuration.
//
// Delivery HMAC governs delivery.requested event signing. The server signs
// every outbound POST with HMAC-SHA256 over a canonical STRING
//
//	<event_timestamp>.<event_id>.<raw_body>
//
// using DeliveryHMACSecret (current). DeliveryHMACSecretPrevious is the
// just-deprecated secret kept around for a rotation window so receivers
// that haven't yet rolled their key cache can keep verifying. Rotation
// flow:
//
//  1. Receiver gets a new secret in their secret manager / vault.
//  2. Receiver rolls their verifier to accept (new + old).
//  3. Operator updates DeliveryHMACSecret here, moves the OLD value to
//     DeliveryHMACSecretPrevious.
//  4. Receivers having rolled can stop accepting the old secret.
//
// Replay protection: every signed POST includes X-Velox-Timestamp so
// receivers enforce a 5-minute window by default (see DeliveryReplayWindow).
// The server does not enforce replay rejection itself — that's the
// receiver's job; the server only makes the timestamp available so the
// receiver can compare against current time.
//
// Production rule (enforced in config.Validate): DeliveryHMACSecret MUST
// be ≥32 bytes (256 bits). Only escape hatch: VELOX_ALLOW_INSECURE_DEV=true.
//
// Tokens (admin / worker) are distinct from HMAC secrets and have their
// own placeholder-rejection patterns.
type SecurityConfig struct {
	AdminToken                 string   `yaml:"admin_token" env:"VELOX_ADMIN_TOKEN" default:""`
	WorkerToken                string   `yaml:"worker_token" env:"VELOX_WORKER_TOKEN" default:""`
	WebhookSecret              string   `yaml:"webhook_secret" env:"VELOX_WEBHOOK_SECRET" default:""`
	EnableAuth                 bool     `yaml:"enable_auth" env:"VELOX_ENABLE_AUTH" default:"true"`
	CORSOrigins                []string `yaml:"cors_origins" env:"VELOX_CORS_ORIGINS" default:"[]"`
	RateLimitEnabled           bool     `yaml:"rate_limit_enabled" env:"VELOX_RATE_LIMIT_ENABLED" default:"true"`
	RateLimitRequests          int      `yaml:"rate_limit_requests" env:"VELOX_RATE_LIMIT_REQUESTS" default:"100"`
	AllowedDownloadHosts       []string `yaml:"allowed_download_hosts" env:"VELOX_ALLOWED_DOWNLOAD_HOSTS" default:"[]"`
	DeliveryHMACSecret         string   `yaml:"delivery_hmac_secret" env:"VELOX_DELIVERY_HMAC_SECRET" default:""`
	DeliveryHMACSecretPrevious string   `yaml:"delivery_hmac_secret_previous" env:"VELOX_DELIVERY_HMAC_SECRET_PREVIOUS" default:""`
	DeliveryReplayWindowSec    int      `yaml:"delivery_replay_window_seconds" env:"VELOX_DELIVERY_REPLAY_WINDOW_SECONDS" default:"300"`
	DeliveryInsecureDev        bool     `yaml:"delivery_insecure_dev" env:"VELOX_ALLOW_INSECURE_DEV" default:"false"`
}

// ExternalConfig holds external service configuration.
type ExternalConfig struct {
	OllamaURL            string `yaml:"ollama_url" env:"OLLAMA_ADDR" default:"http://localhost:11434"`
	OllamaModel          string `yaml:"ollama_model" env:"OLLAMA_MODEL" default:"gemma4:e4b"`
	OllamaMetadataModel  string `yaml:"ollama_metadata_model" env:"OLLAMA_METADATA_MODEL" default:""`
	OllamaTimeoutSeconds int    `yaml:"ollama_timeout_seconds" env:"OLLAMA_TIMEOUT" default:"600"`
	YtdlpPath            string `yaml:"ytdlp_path" env:"YTDLP_PATH" default:"yt-dlp"`
	FfmpegPath           string `yaml:"ffmpeg_path" env:"FFMPEG_PATH" default:"ffmpeg"`
	NvidiaAPIKey         string `yaml:"nvidia_api_key" env:"NVIDIA_API_KEY" default:""`
	NvidiaModel          string `yaml:"nvidia_model" env:"NVIDIA_MODEL" default:"stabilityai/sdxl-turbo"`
	NvidiaLocalNIMURL    string `yaml:"nvidia_local_nim_url" env:"NVIDIA_LOCAL_NIM_URL" default:"http://localhost:8000/v1/infer"`

	// VeloxMasterURL is the canonical address of a remote PipelineGen master.
	// Workers and external clients (n8n, Google Flow sidecars, scripts)
	// read this env var (VELOX_MASTER_URL) so deployments don't have to
	// hardcode hosts. Defaults to http://127.0.0.1:8000 for local dev.
	//
	// Compose/Docker patterns:
	//   - Docker Compose service:  http://velox-server:8000
	//   - Master on host, worker  in Docker: http://host.docker.internal:8000
	//     (Linux requires extra_hosts: ["host.docker.internal:host-gateway"])
	//   - Local dev:  http://127.0.0.1:8000
	VeloxMasterURL string `yaml:"velox_master_url" env:"VELOX_MASTER_URL" default:"http://127.0.0.1:8000"`

	// VeloxBaseURL is the publicly routable URL of THIS PipelineGen
	// server, used by the image service to construct webhook_url for
	// remote image generation callbacks. Distinct from VeloxMasterURL
	// (which is the broker the worker speaks to): VeloxBaseURL is the
	// hostname remote-sidecar clients use to reach us.
	VeloxBaseURL string `yaml:"velox_base_url" env:"VELOX_BASE_URL" default:""`

	// Remote image endpoint (Google Flow on remote server).
	RemoteImageEndpointURL string `yaml:"remote_image_endpoint_url" env:"REMOTE_IMAGE_ENDPOINT_URL" default:""`
	UseNvidiaForLLM        bool   `yaml:"use_nvidia_for_llm" env:"VELOX_USE_NVIDIA_FOR_LLM" default:"false"`
	NvidiaLLMModel         string `yaml:"nvidia_llm_model" env:"VELOX_NVIDIA_LLM_MODEL" default:"meta/llama-3.1-8b-instruct"`
	PixabayAPIKey          string `yaml:"pixabay_api_key" env:"PIXABAY_API_KEY" default:""`
	PixabayBaseURL         string `yaml:"pixabay_base_url" env:"PIXABAY_BASE_URL" default:"https://pixabay.com/api"`
	PexelsAPIKey           string `yaml:"pexels_api_key" env:"PEXELS_API_KEY" default:""`
	PexelsBaseURL          string `yaml:"pexels_base_url" env:"PEXELS_BASE_URL" default:"https://api.pexels.com/v1"`

	// NodeScraperDir directory containing the Node.js scraper scripts (artlist_search.js, etc.).
	// Default "node-scraper" relative to working dir.
	NodeScraperDir string `yaml:"node_scraper_dir" env:"VELOX_NODE_SCRAPER_DIR" default:"node-scraper"`

	// YouTube cookies + JS runtime for yt-dlp.
	YouTubeCookiesPath   string `yaml:"youtube_cookies_path" env:"YT_COOKIES_PATH" default:"cookies.txt"`
	YouTubeJSRuntimePath string `yaml:"youtube_js_runtime_path" env:"YT_JS_RUNTIME_PATH" default:"node"`

	// SearXNG — strictly OPTIONAL sidecar for LLM RAG augmentation.
	//
	// Default URL is the canonical SearXNG dev URL (port 18080). The
	// runtime only calls SearXNG when:
	//
	//   1. SEARXNG_URL is non-empty after env + yaml resolution, AND
	//   2. The configured URL responds to /health at startup (see
	//      composeIntegration's SearXNG probe). If the URL is unreachable
	//      the server logs WARN and disables web-search features without
	//      failing the boot — jobs that REQUIRE SearXNG then return
	//      `provider_not_configured` (overnight-error contract, see
	//      provider_sync.go).
	//
	// Operators that don't use SearXNG should leave SEARXNG_URL at default
	// and skip starting the sidecar (most production deployments). The
	// system reports "SearXNG unavailable" in /api/system/doctor and the
	// affected code paths are documented in AGENTS.md.
	SearxngURL              string `yaml:"searxng_url" env:"SEARXNG_URL" default:"http://127.0.0.1:18080"`
	SearxngMaxResults       int    `yaml:"searxng_max_results"     env:"SEARXNG_MAX_RESULTS"     default:"5"`
	WebSearchTimeoutSeconds int    `yaml:"web_search_timeout_seconds" env:"SEARXNG_TIMEOUT" default:"15"`

	// Artlist scraper optimizations
	ArtlistScraperServerURL        string `yaml:"artlist_scraper_server_url" env:"ARTLIST_SCRAPER_SERVER_URL" default:""`
	ArtlistLiveSearchCacheTTLHours int    `yaml:"artlist_live_search_cache_ttl_hours" env:"ARTLIST_CACHE_TTL_HOURS" default:"24"`
}

// ResolvedYtdlpPath returns the configured yt-dlp path, falling back to "yt-dlp" if empty.
func (c *ExternalConfig) ResolvedYtdlpPath() string {
	if c.YtdlpPath != "" {
		return c.YtdlpPath
	}
	return "yt-dlp"
}

// ResolvedMasterURL returns the configured master URL, falling back to
// the canonical dev default (127.0.0.1:8000) if empty. The default aligns
// with ServerConfig.Port so workers running locally connect to the
// in-process server without explicit config.
func (c *ExternalConfig) ResolvedMasterURL() string {
	if c.VeloxMasterURL != "" {
		return c.VeloxMasterURL
	}
	return "http://127.0.0.1:8000"
}

// PathsConfig holds the few filesystem paths still used by the minimal server.
type PathsConfig struct {
	CredentialsFile  string `yaml:"credentials_file" env:"VELOX_CREDENTIALS_FILE" default:"credentials.json"`
	TokenFile        string `yaml:"token_file" env:"VELOX_TOKEN_FILE" default:"token.json"`
	ClipTextDir      string `yaml:"clip_text_dir" env:"VELOX_CLIP_TEXT_DIR" default:""`
	PythonScriptsDir string `yaml:"python_scripts_dir" env:"VELOX_PYTHON_SCRIPTS_DIR" default:"scripts"`
	WorkflowsDir     string `yaml:"workflows_dir" env:"VELOX_WORKFLOWS_DIR" default:"./workflows"`
}

// WorkersConfig holds worker-related configuration.
type WorkersConfig struct {
	AllowedIPs              []string `yaml:"allowed_ips" default:"[]"`
	HeartbeatTimeout        int      `yaml:"heartbeat_timeout" default:"30"`
	WorkerFailWindowSeconds int      `yaml:"worker_fail_window_seconds" default:"300"`
	WorkerFailThreshold     int      `yaml:"worker_fail_threshold" default:"3"`
}

// FeaturesConfig controls optional modules.
// Stable modules default to true only if their dependencies are available.
// Experimental modules default to false.
type FeaturesConfig struct {
	ArtlistEnabled            bool `yaml:"artlist_enabled" env:"VELOX_FEATURE_ARTLIST_ENABLED" default:"false"`
	YouTubeEnabled            bool `yaml:"youtube_enabled" env:"VELOX_FEATURE_YOUTUBE_ENABLED" default:"false"`
	DriveEnabled              bool `yaml:"drive_enabled" env:"VELOX_FEATURE_DRIVE_ENABLED" default:"false"`
	ScriptDocsEnabled         bool `yaml:"script_docs_enabled" env:"VELOX_FEATURE_SCRIPT_DOCS_ENABLED" default:"false"`
	ScriptClipsEnabled        bool `yaml:"script_clips_enabled" env:"VELOX_FEATURE_SCRIPT_CLIPS_ENABLED" default:"false"`
	VoiceoverEnabled          bool `yaml:"voiceover_enabled" env:"VELOX_FEATURE_VOICEOVER_ENABLED" default:"false"`
	ImagesEnabled             bool `yaml:"images_enabled" env:"VELOX_FEATURE_IMAGES_ENABLED" default:"false"`
	StockPipelineEnabled      bool `yaml:"stock_pipeline_enabled" env:"VELOX_FEATURE_STOCK_PIPELINE_ENABLED" default:"false"`
	CatalogScriptVectorSearch bool `yaml:"catalog_script_vector_search" env:"VELOX_FEATURE_CATALOG_SCRIPT_VECTOR_SEARCH" default:"false"`
}

// ToDatabaseStorageConfig projects this StorageConfig into the
// storage.StorageConfig consumed by `internal/infrastructure/database.OpenSet`.
// The two are deliberately separate types so `database` does not import
// `config` (avoids a cycle: config <-> database).
func (s StorageConfig) ToDatabaseStorageConfig() interface {
	DataDir() string
	PrimaryDBPath() string
	ObservabilityDBPath() string
	WorkspaceDir() string
	CacheDir() string
	ExportDir() string
} {
	return storageSetAdapter{s: s}
}

// Path resolution helpers — used by internal/app/bootstrap.go and any
// subsystem that needs the canonical disk layout under DataDir.
func (s StorageConfig) PrimaryDBFullPath() string {
	if s.PrimaryDBPath != "" {
		return s.PrimaryDBPath
	}
	return s.FullPath(filepath.Join(s.MediaDir, "media.db.sqlite"))
}
func (s StorageConfig) ObservabilityDBFullPath() string {
	if s.ObservabilityDBPath != "" {
		return s.ObservabilityDBPath
	}
	return s.FullPath("observability/api_requests.db.sqlite")
}
func (s StorageConfig) WorkspaceFullPath() string {
	if s.WorkspaceDir != "" {
		return s.WorkspaceDir
	}
	return s.FullPath("workspace")
}
func (s StorageConfig) CacheFullPath() string {
	if s.CacheDir != "" {
		return s.CacheDir
	}
	return s.FullPath("cache")
}
func (s StorageConfig) ExportFullPath() string {
	if s.ExportDir != "" {
		return s.ExportDir
	}
	return s.FullPath("export")
}

type storageSetAdapter struct {
	s StorageConfig
}

func (a storageSetAdapter) DataDir() string {
	if a.s.DataDir == "" { return "data" }
	return a.s.DataDir
}
func (a storageSetAdapter) PrimaryDBPath() string { return a.s.PrimaryDBFullPath() }
func (a storageSetAdapter) ObservabilityDBPath() string { return a.s.ObservabilityDBFullPath() }
func (a storageSetAdapter) WorkspaceDir() string { return a.s.WorkspaceFullPath() }
func (a storageSetAdapter) CacheDir() string { return a.s.CacheFullPath() }
func (a storageSetAdapter) ExportDir() string { return a.s.ExportFullPath() }
