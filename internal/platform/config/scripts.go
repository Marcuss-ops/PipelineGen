package config

// ScriptCapabilityConfig gates the ScriptFlow module wiring. In production
// (DeliveryInsecureDev=false) a required dependency that is missing aborts
// the boot. In development mode the module is disabled explicitly and no
// route is registered.
type ScriptCapabilityConfig struct {
	// Enabled turns the ScriptFlow module on. Default true so existing
	// deployments keep working; set to false to disable explicitly.
	Enabled bool `yaml:"enabled" env:"VELOX_SCRIPTS_CAPABILITY_ENABLED" default:"true"`

	// RequireAI makes a non-nil AI bundle (ScriptEngine) mandatory.
	RequireAI bool `yaml:"require_ai" env:"VELOX_SCRIPTS_REQUIRE_AI" default:"true"`

	// RequireDrive makes a non-nil Drive bundle mandatory.
	RequireDrive bool `yaml:"require_drive" env:"VELOX_SCRIPTS_REQUIRE_DRIVE" default:"true"`

	// RequireDatabase makes a non-nil SQLite DB mandatory. When false,
	// the script-generation run repository is skipped, but routes that
	// depend on it may return errors at runtime.
	RequireDatabase bool `yaml:"require_database" env:"VELOX_SCRIPTS_REQUIRE_DATABASE" default:"true"`
}

// ScriptsConfig holds tunables for the unified script generation endpoints
// (POST /api/script/generate and batch generation).
//
// Why centralize these here:
//   - Batch web search concurrency and chapter generation concurrency were
//     previously hard-coded as `make(chan struct{}, 4)` and `make(chan
//     struct{}, 3)` inside batch_websearch.go / batch_chapters.go. Operators
//     had to recompile to tune them for their Ollama / SearXNG latency.
//
// Defaults match the previous hard-coded values to preserve behavior.
type ScriptsConfig struct {
	// Defaults is the canonical script-generation default set. It is
	// resolved after YAML and environment overrides during bootstrap.
	Defaults ScriptDefaultsConfig `yaml:"defaults"`

	// BatchWebSearchConcurrency caps the parallel SearXNG searches in
	// batch generation. Each search is a network call; 4 is a
	// safe default for a single SearXNG instance. Raise for a clustered
	// SearXNG deployment, lower if you see 429s from SearXNG.
	BatchWebSearchConcurrency int `yaml:"batch_web_search_concurrency" env:"VELOX_SCRIPTS_BATCH_WEBSEARCH_CONCURRENCY" default:"4"`

	// BatchChapterConcurrency caps the parallel Ollama chapter generations
	// in batch generation. Each goroutine sends one LLM call to
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

	// ScriptSegmentDefault (PR-CS-1 FASE 14, July 2026 — CUTOVER default).
	// True ⇒ the canonical POST /api/script/generate path emits a
	// Branch A prompt (ScriptSegment-driven render). False ⇒ legacy
	// Branch B (SegmentTopics-driven render) is the fallback path,
	// governed by the WAVE-21/22 deprecation timeline landing in
	// architecture/deprecations.yaml#DL-SCRIPT-BRANCH-B-001.
	ScriptSegmentDefault bool `yaml:"script_segment_default" env:"VELOX_SCRIPTS_SEGMENT_DEFAULT" default:"true"`

	// DefaultDurationSeconds is the default script duration in seconds.
	// 600 = 10 minutes (standard YouTube clip). Used by /generate and batch jobs.
	DefaultDurationSeconds int `yaml:"default_duration_seconds" env:"VELOX_SCRIPTS_DEFAULT_DURATION" default:"600"`

	// GenerateTimeoutSeconds overrides the default request timeout for
	// POST /api/script/generate (10 min). 0 = use endpoint default.
	GenerateTimeoutSeconds int `yaml:"generate_timeout_seconds" env:"VELOX_SCRIPTS_GENERATE_TIMEOUT" default:"600"`

	// BatchTimeoutSeconds overrides the default request timeout for
	// batch generation (30 min sync) and individual chapters.
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

	// BatchChannelID is the default memory-gate channel for batch generation.
	BatchChannelID string `yaml:"batch_channel_id" env:"VELOX_SCRIPTS_BATCH_CHANNEL_ID" default:"default-batch"`

	// ClipSearchMinScore is the minimum similarity score for clip suggestions
	// returned in insights (phrase clips, intro clips).
	ClipSearchMinScore float64 `yaml:"clip_search_min_score" env:"VELOX_SCRIPTS_CLIP_SEARCH_MIN_SCORE" default:"0.7"`

	// SaveTimeoutSeconds is the context timeout for DB persistence operations
	// in engine.Generate. These run in a background context so they survive
	// HTTP disconnection.
	SaveTimeoutSeconds int `yaml:"save_timeout_seconds" env:"VELOX_SCRIPTS_SAVE_TIMEOUT" default:"30"`

	// ── Source text limits for /api/script/generate ────────────────────
	// MaxSourceTextChars caps the source_text field length in characters.
	MaxSourceTextChars int `yaml:"max_source_text_chars" env:"VELOX_SCRIPTS_MAX_SOURCE_TEXT_CHARS" default:"50000"`
	// MaxSourceTextBytes caps the source_text field length in bytes.
	MaxSourceTextBytes int `yaml:"max_source_text_bytes" env:"VELOX_SCRIPTS_MAX_SOURCE_TEXT_BYTES" default:"100000"`
	// MaxSourceTextTokens caps the estimated token count for source_text.
	MaxSourceTextTokens int `yaml:"max_source_text_tokens" env:"VELOX_SCRIPTS_MAX_SOURCE_TEXT_TOKENS" default:"10000"`
	// MaxSourceTextToTargetWordsRatio caps source_text words / target_words.
	MaxSourceTextToTargetWordsRatio float64 `yaml:"max_source_text_to_target_words_ratio" env:"VELOX_SCRIPTS_MAX_SOURCE_TEXT_TO_TARGET_WORDS_RATIO" default:"5.0"`

	// WordsPerSecondClipEvidence is the max source_text words supported
	// per second of resolved clip evidence duration. Used to reject
	// source_text that exceeds what the clip evidence can plausibly
	// support (SOURCE_TEXT_EXCEEDS_CLIP_EVIDENCE).
	WordsPerSecondClipEvidence float64 `yaml:"words_per_second_clip_evidence" env:"VELOX_SCRIPTS_WORDS_PER_SECOND_CLIP_EVIDENCE" default:"2.5"`

	// LogSourceTextPreview controls whether a short preview of the
	// source_text is included in structured logs. When false, only
	// hash, length and token estimates are logged.
	LogSourceTextPreview bool `yaml:"log_source_text_preview" env:"VELOX_SCRIPTS_LOG_SOURCE_TEXT_PREVIEW" default:"true"`

	// SourceTextPreviewChars caps the source_text preview length in
	// characters when LogSourceTextPreview is true. 0 falls back to 80.
	SourceTextPreviewChars int `yaml:"source_text_preview_chars" env:"VELOX_SCRIPTS_SOURCE_TEXT_PREVIEW_CHARS" default:"80"`

	// MaxSegmentsCap caps the number of ScriptSegment entries the
	// payload validator accepts on POST /api/script/generate. Operators
	// raise for batch generation, lower to enforce strict per-script
	// budget. Default 50 — chosen to comfortably exceed typical
	// 4-12-per-script ranges (news/gossip, multi-act narratives)
	// while preventing pathological payloads from poisoning the
	// engine prompt. PR-CS-1 / FASE 6 (DoD #8).
	MaxSegmentsCap int `yaml:"max_segments_cap" env:"VELOX_SCRIPTS_MAX_SEGMENTS_CAP" default:"50"`

	// SegmentWordsTolerancePercent derives per-segment bounds when a
	// segment does not provide explicit min_words/max_words. Default 15.
	SegmentWordsTolerancePercent float64 `yaml:"segment_words_tolerance_percent" env:"VELOX_SCRIPTS_SEGMENT_WORDS_TOLERANCE_PERCENT" default:"15"`
	// TotalWordsTolerancePercent derives the aggregate script bounds.
	// Default 10, matching the strict final script contract.
	TotalWordsTolerancePercent float64 `yaml:"total_words_tolerance_percent" env:"VELOX_SCRIPTS_TOTAL_WORDS_TOLERANCE_PERCENT" default:"10"`
	// MaxSegmentRegenerationAttempts bounds retries after the initial
	// generation. Default 2 (at most three provider calls total).
	MaxSegmentRegenerationAttempts int `yaml:"max_segment_regeneration_attempts" env:"VELOX_SCRIPTS_MAX_SEGMENT_REGENERATION_ATTEMPTS" default:"2"`

	// ScriptDocsFolderID is the canonical default Google Drive folder for
	// script documents (env PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID). Precedence:
	// explicit payload docs.folder_id > this configured default > fail
	// closed when docs.enabled=true and still empty (the folder must never
	// be invented by an agent or worker). Empty default means "not
	// configured" — resolve via the canonical script docs resolver.
	ScriptDocsFolderID string `yaml:"script_docs_folder_id" env:"PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID" default:""`

	// NLPConcurrency bounds the concurrent SceneAnalysis (entity / important
	// phrase / important word extraction) scenes in the incremental VidRush
	// pipeline — it is the generation-gate capacity. Default 4 (certified).
	// Lower it for a CPU-constrained Ollama host; raise it for a clustered
	// extraction backend. Values <= 0 fall back to the certified default.
	NLPConcurrency int `yaml:"nlp_concurrency" env:"VELOX_SCRIPTS_NLP_CONCURRENCY" default:"4"`

	// TTSConcurrency bounds the concurrent voiceover synthesis calls in the
	// script-generation voiceover phase (the TTS worker pool). When 0
	// (unset) the runner falls back to the voiceover provider bound
	// (VELOX_VOICEOVER_MAX_CONCURRENT_TTS) and then the certified default (4).
	// Note: the low-level TTS provider semaphore (voiceover.max_concurrent_tts)
	// is still the hard ceiling — raising this above that bound only
	// over-schedules, it never oversubscribes the synthesizer.
	TTSConcurrency int `yaml:"tts_concurrency" env:"VELOX_SCRIPTS_TTS_CONCURRENCY"`

	// SerialMode reproduces the pre-parallel "before" chain for controlled
	// benchmarking: the VidRush/NLP branch completes blocking BEFORE TTS
	// (entities → voiceover, never overlapping), and the NLP extraction + TTS
	// pools are forced to concurrency 1. Default false (the parallel
	// SceneTextReady DAG).
	SerialMode bool `yaml:"serial_mode" env:"PIPELINEGEN_SCRIPT_SERIAL_MODE" default:"false"`

	// Capability gates ScriptFlow wiring.
	Capability ScriptCapabilityConfig `yaml:"capability"`
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
	if s.NLPConcurrency <= 0 {
		s.NLPConcurrency = 4
	}
	// TTSConcurrency is intentionally not defaulted here: 0 means "defer to
	// the voiceover provider bound", resolved at the capability wiring
	// boundary (internal/app/capabilities/script_generation_runtime.go).
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
	if s.MaxSourceTextChars <= 0 {
		s.MaxSourceTextChars = 50000
	}
	if s.MaxSourceTextBytes <= 0 {
		s.MaxSourceTextBytes = 100000
	}
	if s.MaxSourceTextTokens <= 0 {
		s.MaxSourceTextTokens = 10000
	}
	if s.MaxSourceTextToTargetWordsRatio <= 0 {
		s.MaxSourceTextToTargetWordsRatio = 5.0
	}
	if s.SourceTextPreviewChars <= 0 {
		s.SourceTextPreviewChars = 80
	}
	if s.MaxSegmentsCap <= 0 {
		s.MaxSegmentsCap = 50
	}
	if s.SegmentWordsTolerancePercent <= 0 {
		s.SegmentWordsTolerancePercent = 15
	}
	if s.TotalWordsTolerancePercent <= 0 {
		s.TotalWordsTolerancePercent = 10
	}
	if s.MaxSegmentRegenerationAttempts <= 0 {
		s.MaxSegmentRegenerationAttempts = 2
	}
	return s
}
