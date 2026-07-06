// Package config provides configuration management for the PipelineGen system.
package config

// ConcurrencyConfig holds system-wide concurrency limits for resource-bound
// operations. Every limit was previously hardcoded as a package-level channel
// capacity or a const (values 1–3). For a 100-worker deployment these defaults
// have been raised to ≥10 so workers don't serialise behind a single bottleneck.
// Each field accepts a zero value (0) to disable the limit entirely, though
// disabling video extraction or script generation is rarely useful in practice.
type ConcurrencyConfig struct {
	// MaxConcurrentVideoExtracts limits parallel yt-dlp download + cut operations.
	// Was hardcoded at 1 (SQLite lock serialisation); WAL mode now allows ≥10.
	// June 2026 GPU tuning: lowered from 10 to 2 to avoid I/O oversubscription.
	MaxConcurrentVideoExtracts int `yaml:"max_concurrent_video_extracts" env:"VELOX_CONCURRENT_VIDEO_EXTRACTS" default:"2"`

	// MaxConcurrentScriptGenerations limits concurrent LLM script generation.
	// Was hardcoded at 2; raised to 50 for 100-worker parallelism.
	// June 2026 GPU tuning: lowered from 50 to 2 to avoid RAM saturation.
	MaxConcurrentScriptGenerations int `yaml:"max_concurrent_script_generations" env:"VELOX_CONCURRENT_SCRIPT_GENERATIONS" default:"2"`

	// MaxConcurrentNvidiaGenerations limits concurrent GPU image generation requests.
	// Was hardcoded at 2; raised to 10 (VRAM-bound).
	MaxConcurrentNvidiaGenerations int `yaml:"max_concurrent_nvidia_generations" env:"VELOX_CONCURRENT_NVIDIA_GENERATIONS" default:"10"`

	// MaxConcurrentOllamaCalls limits concurrent Ollama model invocations.
	// Was hardcoded at 2; raised to 50 (model server should handle this load).
	// June 2026 GPU tuning: lowered from 50 to 1 to avoid RAM saturation on single GPU.
	MaxConcurrentOllamaCalls int `yaml:"max_concurrent_ollama_calls" env:"VELOX_CONCURRENT_OLLAMA_CALLS" default:"1"`

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
	Reranker         RerankerConfig         `yaml:"reranker"`
	Books            BooksConfig            `yaml:"books"`
	VLM              VLMConfig              `yaml:"vlm"`
	Lessons          LessonsConfig          `yaml:"lessons"`
	Multilingual     MultilingualConfig     `yaml:"multilingual"`
	Scripts          ScriptsConfig          `yaml:"scripts"`
	Outbox           OutboxConfig           `yaml:"outbox"`
	Qdrant           QdrantConfig           `yaml:"qdrant"`
}

// AuthSecurityPort compatibility helpers. These keep the application-layer
// middleware ports satisfied without forcing the router to construct an
// extra adapter layer.
func (c *Config) EnableAuth() bool {
	if c == nil {
		return false
	}
	return c.Security.EnableAuth
}

func (c *Config) AdminToken() string {
	if c == nil {
		return ""
	}
	return c.Security.AdminToken
}

func (c *Config) WorkerToken() string {
	if c == nil {
		return ""
	}
	return c.Security.WorkerToken
}

// RateLimitPort compatibility helpers.
func (c *Config) RateLimitEnabled() bool {
	if c == nil {
		return false
	}
	return c.Security.RateLimitEnabled
}

func (c *Config) RateLimitRequests() int {
	if c == nil {
		return 0
	}
	return c.Security.RateLimitRequests
}

// FeatureFlagsPort compatibility helpers.
func (c *Config) ArtlistEnabled() bool {
	if c == nil {
		return false
	}
	return c.Features.ArtlistEnabled
}

func (c *Config) ScriptDocsEnabled() bool {
	if c == nil {
		return false
	}
	return c.Features.ScriptDocsEnabled
}

func (c *Config) ScriptClipsEnabled() bool {
	if c == nil {
		return false
	}
	return c.Features.ScriptClipsEnabled
}

// GoogleAccountingConfig configures the Google Accounting sidecar used for
// AI-video flows. The sidecar is OFF by default; when Enabled=true the
// pipeline routes per-clip image and AI-avatar requests through it and
