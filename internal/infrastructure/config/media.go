package config

// ClipIndexerConfig holds configuration for the ClipIndexer service.
// It provides the URL and script path for the Python-based indexing pipeline.
type ClipIndexerConfig struct {
	Enabled               bool   `yaml:"enabled" default:"true"`
	ServerURL             string `yaml:"server_url" default:"http://127.0.0.1:8001"`
	ScriptPath            string `yaml:"script_path" default:"scripts/bridges/index_clips.py"`
	PythonBin             string `yaml:"python_bin" default:"python3"`
	AutoIndexAfterArtlist bool   `yaml:"auto_index_after_artlist" default:"true"`
}

// VectorSearchConfig holds settings for the vector search (Qdrant) integration.
// CollectionAliasPattern:
//
//	When UseCollectionAlias is true (default), the system creates a versioned
//	collection (e.g. pipelinegen_clips_v1) and an alias (e.g. pipelinegen_clips_current).
//	This enables zero-downtime migration: backfill v2, then switch the alias.
type VectorSearchConfig struct {
	Enabled              bool    `yaml:"enabled" default:"false"`
	Provider             string  `yaml:"provider" default:"qdrant"`
	URL                  string  `yaml:"url" default:"http://127.0.0.1:6333"`
	Collection           string  `yaml:"collection" default:"media_assets"`
	TextVectorName       string  `yaml:"text_vector_name" default:"text"`
	VisualVectorName     string  `yaml:"visual_vector_name" default:"visual"`
	AudioVectorName      string  `yaml:"audio_vector_name" default:"audio"`
	TranscriptVectorName string  `yaml:"transcript_vector_name" default:"transcript"`
	SparseVectorName     string  `yaml:"sparse_vector_name" default:"bm25_text"`
	TextDimensions       int     `yaml:"text_dimensions" default:"768"`
	VisualDimensions     int     `yaml:"visual_dimensions" default:"512"`
	AudioDimensions      int     `yaml:"audio_dimensions" default:"512"`
	TranscriptDimensions int     `yaml:"transcript_dimensions" default:"768"`
	MinInstantScore      float64 `yaml:"min_instant_score" default:"0.85"`
	TimeoutMs            int     `yaml:"timeout_ms" default:"5000"`
	GRPCPort             int     `yaml:"grpc_port" default:"6334"`
	RealtimeEnabled      bool    `yaml:"realtime_enabled" default:"false"`
	AllowBackgroundGen   bool    `yaml:"allow_background_generation" default:"false"`
	// RetryAttempts is the number of attempts (including the first try) for
	// transient Qdrant errors (5xx, 429, network). 1 disables retries.
	// Default 3: two retries after the first failure.
	RetryAttempts int `yaml:"retry_attempts" env:"VELOX_QDRANT_RETRY_ATTEMPTS" default:"3"`
	// RetryInitialWaitMs is the backoff before the first retry, in ms.
	// Default 200ms; doubles each attempt up to RetryMaxWaitMs.
	RetryInitialWaitMs int `yaml:"retry_initial_wait_ms" env:"VELOX_QDRANT_RETRY_INITIAL_WAIT_MS" default:"200"`
	// RetryMaxWaitMs caps the exponential backoff between retries, in ms.
	// Default 5000ms (5s) — long enough to ride out a brief Qdrant restart.
	RetryMaxWaitMs int `yaml:"retry_max_wait_ms" env:"VELOX_QDRANT_RETRY_MAX_WAIT_MS" default:"5000"`

	// CollectionVersion is the schema version suffix appended to the
	// physical Qdrant collection (e.g. "v2" → "{Collection}_v2"). When set,
	// reads and writes route through the alias `{Collection}_current`
	// (or CollectionAlias) so that schema bumps become zero-downtime
	// operations: create the new version, backfill, then SwitchAlias.
	// Leave empty for legacy mode that operates directly on Collection.
	CollectionVersion string `yaml:"collection_version" env:"VELOX_QDRANT_COLLECTION_VERSION" default:""`
	// CollectionAlias overrides the default `{Collection}_current` alias
	// name. Useful when multiple PipelineGen clusters share one Qdrant
	// and need distinct alias names per cluster.
	CollectionAlias string `yaml:"collection_alias" env:"VELOX_QDRANT_COLLECTION_ALIAS" default:""`
	// DisableAlias forces reads and writes to target the versioned
	// collection directly instead of the alias. Use only for one-off
	// backfill scripts against a specific versioned collection; never
	// set in normal operation since it bypasses zero-downtime migration.
	DisableAlias bool `yaml:"disable_alias" env:"VELOX_QDRANT_DISABLE_ALIAS" default:"false"`
}

// RerankerConfig holds settings for the CrossEncoder reranking service.
// The reranker is an optional post-Qdrant reordering layer that improves
// semantic precision for all media types (clips, stock, artlist, images, voiceovers).
type RerankerConfig struct {
	Enabled   bool    `yaml:"enabled" default:"false"`
	URL       string  `yaml:"url" default:"http://127.0.0.1:8091/rerank"`
	Model     string  `yaml:"model" default:"BAAI/bge-reranker-v2-m3"`
	TopK      int     `yaml:"top_k" default:"30"`
	TimeoutMs int     `yaml:"timeout_ms" default:"150"`
	Weight    float64 `yaml:"weight" default:"0.35"`
}

// BooksConfig holds settings for the book summarization/processing service.
type BooksConfig struct {
	Enabled       bool   `yaml:"enabled" default:"true"`
	ScriptPath    string `yaml:"script_path" default:"scripts/bridges/book_summarizer.py"`
	PythonBin     string `yaml:"python_bin" default:"python3"`
	DefaultModel  string `yaml:"default_model" default:"gemma4:e4b"`
	OllamaURL     string `yaml:"ollama_url" default:"http://127.0.0.1:11434"`
	PagesPerChunk int    `yaml:"pages_per_chunk" default:"4"`
	ChunkSize     int    `yaml:"chunk_size" default:"12000"`
}

// VLMConfig holds settings for the VLM (Vision-Language Model) sidecar integration.
type VLMConfig struct {
	Enabled   bool    `yaml:"enabled" default:"true"`
	URL       string  `yaml:"url" default:"http://localhost:8000"`
	Model     string  `yaml:"model" default:"nvidia/nemotron-nano-12b-v2-vl:free"`
	TimeoutMs int     `yaml:"timeout_ms" default:"120000"`
	Weight    float64 `yaml:"weight" default:"0.3"`
}

// LessonsConfig holds settings for the lesson generation service.
type LessonsConfig struct {
	Enabled             bool   `yaml:"enabled" default:"true"`
	DefaultModel        string `yaml:"default_model" default:"gemma4:e4b"`
	DefaultTone         string `yaml:"default_tone" default:"educational"`
	DefaultLanguage     string `yaml:"default_language" default:"it"`
	DefaultImageModel   string `yaml:"default_image_model" default:"flux-1-dev"`
	MaxParallelChapters int    `yaml:"max_parallel_chapters" default:"5"`
}

// MultilingualConfig holds settings for multilingual metadata generation.
// When enabled, the semantic tagger will translate key metadata fields
// (search_text, tags, subjects, mood) into the configured languages
// at ingest time via Ollama, storing translations in metadata.json.
type MultilingualConfig struct {
	Enabled            bool     `yaml:"enabled" default:"false"`
	SourceLanguage     string   `yaml:"source_language" default:"en"`
	TranslateLanguages []string `yaml:"translate_languages" default:"it, es, fr, de"`
}

// JobsConfig holds job-related configuration.
type JobsConfig struct {
	NewJobsPaused         bool   `yaml:"new_jobs_paused" default:"false"`
	LeaseTTLSeconds       int    `yaml:"lease_ttl_seconds" default:"300"`
	MaxParallelPerProject int    `yaml:"max_parallel_per_project" default:"16"`
	AutoCleanupHours      int    `yaml:"auto_cleanup_hours" default:"24"`
	CatalogSyncInterval   string `yaml:"catalog_sync_interval" env:"VELOX_CATALOG_SYNC_INTERVAL" default:"6h"`
	YouTubeExtractTimeout int    `yaml:"youtube_extract_timeout_seconds" env:"VELOX_YOUTUBE_EXTRACT_TIMEOUT" default:"1200"`
	MaintenanceInterval   string `yaml:"maintenance_interval" default:"24h"`
	BackupInterval        string `yaml:"backup_interval" default:"6h"`
	IndexingInterval      string `yaml:"indexing_interval" default:"15m"`
	RetentionDays         int    `yaml:"retention_days" env:"VELOX_RETENTION_DAYS" default:"30"`
	// SearchRateLimit limits YouTube search API calls per hour for search_queries.
	// 0 = unlimited. Default 10/hour is safe for YouTube free tier (100 units/day).
	SearchRateLimit int `yaml:"search_rate_limit" default:"10"`
	// EnableBackgroundJobs controls whether background workers/schedulers run.
	// Default true; set to false via env VELOX_ENABLE_BACKGROUND_JOBS=false for dev mode.
	EnableBackgroundJobs bool `yaml:"enable_background_jobs" env:"VELOX_ENABLE_BACKGROUND_JOBS" default:"true"`
	// EnableChannelMonitor controls the YouTube channel monitor scheduler.
	// Default false; opt-in via env VELOX_ENABLE_CHANNEL_MONITOR=true.
	EnableChannelMonitor bool `yaml:"enable_channel_monitor" env:"VELOX_ENABLE_CHANNEL_MONITOR" default:"false"`
	// EnableTestJobHandlers registers test-only job handlers (echo, slow, fail).
	// Default false; set via env VELOX_ENABLE_TEST_JOB_HANDLERS=true for dev/testing.
	EnableTestJobHandlers bool `yaml:"enable_test_job_handlers" env:"VELOX_ENABLE_TEST_JOB_HANDLERS" default:"false"`
}
