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

	// MaxConcurrentGoogleSlidesGenerations limits the number of Chrome/Playwright
	// workers used for AI image generation. Each slot is a separate browser
	// process, so keep this small unless the host has headroom.
	MaxConcurrentGoogleSlidesGenerations int `yaml:"max_concurrent_google_slides_generations" env:"VELOX_CONCURRENT_GOOGLE_SLIDES_GENERATIONS" default:"2"`

	// GoogleSlidesProfileID selects the first persistent Chrome profile used
	// by the image-generation pool. Operators can point the pool at an
	// authenticated profile without changing the application binary.
	GoogleSlidesProfileID int `yaml:"google_slides_profile_id" env:"VELOX_GOOGLE_SLIDES_PROFILE_ID" default:"0"`

	// MaxConcurrentChannelChecks limits concurrent YouTube channel monitor checks.
	// Was hardcoded at 3; raised to 20.
	MaxConcurrentChannelChecks int `yaml:"max_concurrent_channel_checks" env:"VELOX_CONCURRENT_CHANNEL_CHECKS" default:"20"`
}

// TranslationConfig holds translation-service policy (FASE 9 VO-OPERATIONAL-READINESS, July 2026).
//
// When Required=true, the voiceover composition root MUST receive a non-nil
// translation.TranslationPort at boot time. A nil port causes a fail-fast
// panic with an actionable error message — no silent fallback to "return text
// unchanged". Default is false (dev mode: translation is optional, missing
// port degrades gracefully).
type TranslationConfig struct {
	// Required gates the translation port at composition time. When true,
	// buildVoiceoverService panics if translationPort is nil. Operators
	// should set this to true in production configs. Default: false.
	Required bool `yaml:"required" default:"false"`
}

// LinguisticsConfig holds the configuration for the centralized
// LexiconRegistry (PR-LEXICON-SSOT, July 2026).
//
// LexiconRoot is the directory that contains per-language subdirectories
// (e.g. config/lexicons/en, config/lexicons/it). Each subdirectory may
// contain stopwords.txt, function_words.txt, entity_blocklist.txt,
// verb_morphology.txt, phrase_policy.txt, negative_particles.txt and
// visual_verbs.txt. An empty root is invalid and fails startup.
//
// RequiredLanguages lists the language codes that MUST have a profile
// under LexiconRoot. If any required language is missing, the boot fails
// explicitly with a clear error — no silent degradation.
type LinguisticsConfig struct {
	LexiconRoot       string   `yaml:"lexicon_root" env:"VELOX_LEXICON_ROOT"`
	RequiredLanguages []string `yaml:"required_languages"`
}

// VoiceoverConcurrencyConfig holds voiceover-pipeline concurrency limits,
// retry budgets, and per-stage timeouts (FASE 8 VO-OPERATIONAL-READINESS, July 2026).
//
// Concurrency limits use channel-based semaphores wired as thin adapters in
// the composition root (build_bundles_voiceover_rate_limits.go). Retry for
// Drive uploads routes through pkg/retry.Do with exponential backoff.
// Per-stage timeouts are applied via context.WithTimeout in the respective
// adapters BEFORE the semaphore acquire so the timeout budget includes both
// the queue-wait and the actual execution.
type VoiceoverConcurrencyConfig struct {
	// Defaults is the immutable voiceover request/pipeline policy resolved
	// during bootstrap. Constructors consume these values; they do not
	// synthesize local replacements when a field is zero.
	Defaults VoiceoverDefaultsConfig `yaml:"defaults"`

	// MaxConcurrentDriveUploads limits parallel Google Drive upload calls
	// across the voiceover pipeline. Recommended range: 2-5.
	// Default: 3.
	MaxConcurrentDriveUploads int `yaml:"max_concurrent_drive_uploads" env:"VELOX_VOICEOVER_MAX_CONCURRENT_DRIVE_UPLOADS" default:"3"`

	// MaxConcurrentTTS limits parallel text-to-speech synthesis calls.
	// TTS is CPU-bound (ffmpeg/edge-tts spawn per-call processes); keeping
	// this at 1-2 avoids I/O oversubscription. Default: 2.
	MaxConcurrentTTS int `yaml:"max_concurrent_tts" env:"VELOX_VOICEOVER_MAX_CONCURRENT_TTS" default:"2"`

	// Drive retry budget.

	// DriveUploadMaxRetries is the maximum number of Drive upload attempts
	// (inclusive) before the call fails permanently. Default: 3.
	DriveUploadMaxRetries int `yaml:"drive_upload_max_retries" env:"VELOX_VOICEOVER_DRIVE_UPLOAD_MAX_RETRIES" default:"3"`

	// DriveUploadRetryBackoffMs is the initial backoff in milliseconds
	// before the first Drive upload retry. The pkg/retry loop applies
	// exponential backoff with factor 2.0 capped at 10s. Default: 1000 (1s).
	DriveUploadRetryBackoffMs int `yaml:"drive_upload_retry_backoff_ms" env:"VELOX_VOICEOVER_DRIVE_UPLOAD_RETRY_BACKOFF_MS" default:"1000"`

	// Per-stage timeout budgets (seconds).

	// TTSTimeoutSec is the per-call timeout for TTS synthesis.
	// Default: 120 (2 min).
	TTSTimeoutSec int `yaml:"tts_timeout_sec" env:"VELOX_VOICEOVER_TTS_TIMEOUT_SEC" default:"120"`

	// DriveUploadTimeoutSec is the per-call timeout for a single Drive
	// upload attempt (NOT the cumulative retry budget). Default: 300 (5 min).
	DriveUploadTimeoutSec int `yaml:"drive_upload_timeout_sec" env:"VELOX_VOICEOVER_DRIVE_UPLOAD_TIMEOUT_SEC" default:"300"`

	// OllamaTimeoutSec is the per-call timeout for Ollama translation calls
	// from the voiceover pipeline. Default: 120 (2 min).
	OllamaTimeoutSec int `yaml:"ollama_timeout_sec" env:"VELOX_VOICEOVER_OLLAMA_TIMEOUT_SEC" default:"120"`

	// TTS retry + circuit breaker (FASE 6, July 2026).

	// TTSMaxRetries is the maximum number of TTS synthesis attempts
	// (inclusive) before the call fails permanently. Default: 3.
	TTSMaxRetries int `yaml:"tts_max_retries" env:"VELOX_VOICEOVER_TTS_MAX_RETRIES" default:"3"`

	// TTSRetryBackoffMs is the initial backoff in milliseconds before
	// the first TTS retry. Exponential backoff with factor 2.0.
	// Default: 500 (0.5s).
	TTSRetryBackoffMs int `yaml:"tts_retry_backoff_ms" env:"VELOX_VOICEOVER_TTS_RETRY_BACKOFF_MS" default:"500"`

	// TTSCircuitBreakerThreshold is the number of consecutive TTS
	// failures after which the circuit breaker opens and TTS calls
	// are rejected immediately (no attempt). Default: 5.
	TTSCircuitBreakerThreshold int `yaml:"tts_circuit_breaker_threshold" env:"VELOX_VOICEOVER_TTS_CIRCUIT_BREAKER_THRESHOLD" default:"5"`

	// TTSCircuitBreakerCooldownMs is the cooldown period in
	// milliseconds before the circuit breaker transitions from
	// open → half-open, allowing a single probe call. Default: 30000 (30s).
	TTSCircuitBreakerCooldownMs int `yaml:"tts_circuit_breaker_cooldown_ms" env:"VELOX_VOICEOVER_TTS_CIRCUIT_BREAKER_COOLDOWN_MS" default:"30000"`
}

// Config holds all configuration for the application.
// All fields are public and read-only after bootstrap. The previous
// sync.RWMutex was decorative (fields were mutated directly without locking)
// so it has been removed to avoid a false guarantee of thread safety.
type Config struct {
	Server           ServerConfig               `yaml:"server"`
	Logging          LoggingConfig              `yaml:"logging"`
	Storage          StorageConfig              `yaml:"storage"`
	Security         SecurityConfig             `yaml:"security"`
	External         ExternalConfig             `yaml:"external"`
	Paths            PathsConfig                `yaml:"paths"`
	Drive            DriveConfig                `yaml:"drive"`
	Concurrency      ConcurrencyConfig          `yaml:"concurrency"`
	Voiceover        VoiceoverConcurrencyConfig `yaml:"voiceover"`
	Translation      TranslationConfig          `yaml:"translation"`
	Linguistics      LinguisticsConfig          `yaml:"linguistics"`
	Jobs             JobsConfig                 `yaml:"jobs"`
	Workers          WorkersConfig              `yaml:"workers"`
	Video            VideoConfig                `yaml:"video"`
	Features         FeaturesConfig             `yaml:"features"`
	GoogleAccounting GoogleAccountingConfig     `yaml:"google_accounting"`
	ClipIndexer      ClipIndexerConfig          `yaml:"clip_indexer"`
	Reranker         RerankerConfig             `yaml:"reranker"`
	Books            BooksConfig                `yaml:"books"`
	VLM              VLMConfig                  `yaml:"vlm"`
	Lessons          LessonsConfig              `yaml:"lessons"`
	Multilingual     MultilingualConfig         `yaml:"multilingual"`
	// Media is the canonical namespace for media-pipeline configuration
	// (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b, July 2026). The nested
	// Media.Multilingual is the SSOT path consumed by the YouTube
	// acquisition chain (buildDomainMediaServices). The top-level
	// Multilingual field is retained for back-compat with pre-Fase-1.b
	// callers; both shapes carry the same MultilingualConfig type.
	Media   MediaConfig   `yaml:"media"`
	Scripts ScriptsConfig `yaml:"scripts"`
	Outbox  OutboxConfig  `yaml:"outbox"`
	Qdrant  QdrantConfig  `yaml:"qdrant"`
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

func (c *Config) ScriptClipsEnabled() bool {
	if c == nil {
		return false
	}
	return c.Features.ScriptClipsEnabled
}

// GoogleAccountingConfig configures the Google Accounting sidecar used for
// AI-video flows. The sidecar is OFF by default; when Enabled=true the
// pipeline routes per-clip image and AI-avatar requests through it and
