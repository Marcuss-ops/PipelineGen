package config

// ScriptsConfig holds tunables for the unified script generation endpoints
// (POST /api/script/generate-batch, /generate-from-clips,
// /generate-with-images → script + Google Slides images).
//
// Why centralize these here:
//   - Batch web search concurrency and chapter generation concurrency were
//     previously hard-coded as `make(chan struct{}, 4)` and `make(chan
//     struct{}, 3)` inside batch_websearch.go / batch_chapters.go. Operators
//     had to recompile to tune them for their Ollama / SearXNG latency.
//
// Defaults match the previous hard-coded values to preserve behavior.
type ScriptsConfig struct {
	// BatchWebSearchConcurrency caps the parallel SearXNG searches in
	// /api/script/generate-batch. Each search is a network call; 4 is a
	// safe default for a single SearXNG instance. Raise for a clustered
	// SearXNG deployment, lower if you see 429s from SearXNG.
	BatchWebSearchConcurrency int `yaml:"batch_web_search_concurrency" env:"VELOX_SCRIPTS_BATCH_WEBSEARCH_CONCURRENCY" default:"4"`

	// BatchChapterConcurrency caps the parallel Ollama chapter generations
	// in /api/script/generate-batch. Each goroutine sends one LLM call to
	// Ollama, which serializes requests internally, so going above the
	// Ollama queue depth just adds latency. 3 is the safe default.
	BatchChapterConcurrency int `yaml:"batch_chapter_concurrency" env:"VELOX_SCRIPTS_BATCH_CHAPTER_CONCURRENCY" default:"3"`

	// MaxBatchWorkers caps the number of concurrent items processed by
	// GenerateManyUseCase. Each worker runs the full unified pipeline
	// (normalize → validate → resolve → build → generate → postprocess)
	// for one item. Default 4.
	MaxBatchWorkers int `yaml:"max_batch_workers" env:"VELOX_SCRIPTS_MAX_BATCH_WORKERS" default:"4"`

	// MaxInsightEntities caps the number of important words, important phrases,
	// special names, and artlist phrases extracted per script. Default 12.
	MaxInsightEntities int `yaml:"max_insight_entities" env:"VELOX_SCRIPTS_MAX_INSIGHT_ENTITIES" default:"12"`

	// ── Default values for request fields ────────────────────────────────

	// DefaultTone is the default tone for script generation (single + batch).
	DefaultTone string `yaml:"default_tone" env:"VELOX_SCRIPTS_DEFAULT_TONE" default:"documentary"`

	// DefaultLanguage is the default language for all generate endpoints.
	DefaultLanguage string `yaml:"default_language" env:"VELOX_SCRIPTS_DEFAULT_LANGUAGE" default:"it"`

	// DefaultDurationSeconds is the default script duration in seconds.
	// 600 = 10 minutes (standard YouTube clip). Used by /generate and /generate-batch.
	DefaultDurationSeconds int `yaml:"default_duration_seconds" env:"VELOX_SCRIPTS_DEFAULT_DURATION" default:"600"`

	// GenerateTimeoutSeconds overrides the default request timeout for
	// POST /api/script/generate (10 min). 0 = use endpoint default.
	GenerateTimeoutSeconds int `yaml:"generate_timeout_seconds" env:"VELOX_SCRIPTS_GENERATE_TIMEOUT" default:"600"`

	// BatchTimeoutSeconds overrides the default request timeout for
	// POST /api/script/generate-batch (30 min sync) and individual chapters.
	BatchTimeoutSeconds int `yaml:"batch_timeout_seconds" env:"VELOX_SCRIPTS_BATCH_TIMEOUT" default:"1800"`

	// MaxTopicChars caps the Topic field length (single generate).
	MaxTopicChars int `yaml:"max_topic_chars" env:"VELOX_SCRIPTS_MAX_TOPIC_CHARS" default:"5000"`

	// MaxGuidelinesChars caps the Guidelines field length.
	MaxGuidelinesChars int `yaml:"max_guidelines_chars" env:"VELOX_SCRIPTS_MAX_GUIDELINES_CHARS" default:"4000"`

	// MaxPhraseSuggestions caps how many ImportantPhrases produce clip
	// suggestions in the insights output.
	MaxPhraseSuggestions int `yaml:"max_phrase_suggestions" env:"VELOX_SCRIPTS_MAX_PHRASE_SUGGESTIONS" default:"5"`

	// MinWordFloor is the minimum word count floor when deriving MinWords
	// from Duration.
	MinWordFloor int `yaml:"min_word_floor" env:"VELOX_SCRIPTS_MIN_WORD_FLOOR" default:"200"`

	// ChannelID is the default memory-gate channel for /api/script/generate.
	ChannelID string `yaml:"channel_id" env:"VELOX_SCRIPTS_CHANNEL_ID" default:"default"`

	// BatchChannelID is the default memory-gate channel for /api/script/generate-batch.
	BatchChannelID string `yaml:"batch_channel_id" env:"VELOX_SCRIPTS_BATCH_CHANNEL_ID" default:"default-batch"`

	// ClipSearchMinScore is the minimum similarity score for clip suggestions
	// returned in insights (phrase clips, intro clips).
	ClipSearchMinScore float64 `yaml:"clip_search_min_score" env:"VELOX_SCRIPTS_CLIP_SEARCH_MIN_SCORE" default:"0.7"`

	// SaveTimeoutSeconds is the context timeout for DB persistence operations
	// in engine.Generate. These run in a background context so they survive
	// HTTP disconnection.
	SaveTimeoutSeconds int `yaml:"save_timeout_seconds" env:"VELOX_SCRIPTS_SAVE_TIMEOUT" default:"30"`
}

// WithDefaults returns a copy of ScriptsConfig with zero-values replaced by
// defaults. Negative values are clamped to 1. The pattern mirrors
// VideoConfig.WithDefaults and LessonsConfig.
func (s ScriptsConfig) WithDefaults() ScriptsConfig {
	if s.BatchWebSearchConcurrency <= 0 {
		s.BatchWebSearchConcurrency = 4
	}
	if s.BatchChapterConcurrency <= 0 {
		s.BatchChapterConcurrency = 3
	}
	if s.MaxBatchWorkers <= 0 {
		s.MaxBatchWorkers = 4
	}
	if s.MaxInsightEntities <= 0 {
		s.MaxInsightEntities = 12
	}
	// Defaults for new centralised fields
	if s.DefaultTone == "" {
		s.DefaultTone = "documentary"
	}
	if s.DefaultLanguage == "" {
		s.DefaultLanguage = "it"
	}
	if s.DefaultDurationSeconds <= 0 {
		s.DefaultDurationSeconds = 600
	}
	if s.GenerateTimeoutSeconds <= 0 {
		s.GenerateTimeoutSeconds = 600
	}
	if s.BatchTimeoutSeconds <= 0 {
		s.BatchTimeoutSeconds = 1800
	}
	if s.MaxTopicChars <= 0 {
		s.MaxTopicChars = 5000
	}
	if s.MaxGuidelinesChars <= 0 {
		s.MaxGuidelinesChars = 4000
	}
	if s.MaxPhraseSuggestions <= 0 {
		s.MaxPhraseSuggestions = 5
	}
	if s.MinWordFloor <= 0 {
		s.MinWordFloor = 200
	}
	if s.ChannelID == "" {
		s.ChannelID = "default"
	}
	if s.BatchChannelID == "" {
		s.BatchChannelID = "default-batch"
	}
	if s.ClipSearchMinScore <= 0 {
		s.ClipSearchMinScore = 0.7
	}
	if s.SaveTimeoutSeconds <= 0 {
		s.SaveTimeoutSeconds = 30
	}
	return s
}
