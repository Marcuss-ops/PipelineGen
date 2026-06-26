package config

// ClipIndexerConfig holds configuration for the ClipIndexer service.
// It provides the URL and script path for the Python-based indexing pipeline.
type ClipIndexerConfig struct {
	Enabled               bool   `yaml:"enabled" default:"true"`
	ServerURL             string `yaml:"server_url" default:"http://127.0.0.1:8001"`
	ScriptPath            string `yaml:"script_path" default:"scripts/bridges/index_clips.py"`
	PythonBin             string `yaml:"python_bin" default:"python3"`
	AutoIndexAfterArtlist bool   `yaml:"auto_index_after_artlist" default:"true"`
	// MaxConcurrentIndexing limits parallel Python subprocesses launched for clip indexing.
	// Delegates to ConcurrencyConfig.MaxConcurrentClipIndexing at wiring time.
	MaxConcurrentIndexing int `yaml:"max_concurrent_indexing" env:"VELOX_CONCURRENT_CLIP_INDEXING" default:"10"`
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
