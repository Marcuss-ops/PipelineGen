package config

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// LanguageSpecSlice is the YAML-facing carrier for
// MultilingualConfig.Languages. It accepts BOTH the
// legacy CSV-shaped YAML
//
//	languages: [it, en, es, ...]
//
// (auto-promoted to enabled+translate+tts on every entry) AND
// the typed struct-list shape
//
//	languages:
//	  - {code: it, enabled: true, translate_clips: true, generate_tts: false}
//	  - {code: en, enabled: true, translate_clips: true, generate_tts: true}
//
// (preserved verbatim). godlike/06 SSOT: this is the SINGLE
// canonical decoder for cfg.MultilingualConfig.Languages.
//
// PR-CATALOG-MULTILINGUA step 3 (July 2026): introduced alongside
// the domain/asset.LanguageRegistry SSOT; legacy pre-step-3
// configs that still carry `materialize_languages:` continue to
// parse via MultilingualConfig.MaterializeLanguages and are
// auto-promoted into a registry by the composition root
// (build_bundles_texttracks.go).
type LanguageSpecSlice []asset.LanguageSpec

// UnmarshalYAML handles both []string (legacy) and []LanguageSpec
// (new) shapes. The struct-list shape is attempted first; on
// success the slice is set verbatim. On any error from the
// struct list, the []string shape is attempted and each code
// is auto-promoted to a fully-enabled spec.
func (l *LanguageSpecSlice) UnmarshalYAML(node *yaml.Node) error {
	// Try the typed struct-list shape first. An empty YAML
	// list decodes to a nil slice without error, which is
	// fine (we'll fall through to the string-list pass and
	// also get an empty list there). The struct-list path
	// wins for non-empty typed YAMLs.
	var structs []asset.LanguageSpec
	if err := node.Decode(&structs); err == nil && len(structs) > 0 {
		*l = structs
		return nil
	}
	// Try the legacy []string shape. yaml.v3 will refuse a
	// map node into a []string, so a non-list YAML surfaces
	// a decode error here (caller bug).
	var codes []string
	if err := node.Decode(&codes); err != nil {
		return fmt.Errorf("multilingual.languages: must be []string (legacy) or []LanguageSpec (typed): %w", err)
	}
	for _, c := range codes {
		*l = append(*l, asset.LanguageSpec{
			Code:           c,
			Enabled:        true,
			TranslateClips: true,
			GenerateTTS:    true,
		})
	}
	return nil
}

// MediaConfig groups all media-pipeline configuration (multilingual
// settings, text-track acquisition, future media-runtime knobs) under a
// single namespace so the Config struct stays tree-readable. The
// nested Multilingual field mirrors the top-level Multilingual on
// Config (kept for back-compat with the pre-Fase-1.b callers); both
// shapes carry the same canonical MultilingualConfig so YAML drift
// between media.multilingual.* and multilingual.* keys is a
// follow-up concern, not a blocker for the Go-level wiring.
//
// godlike/06 SSOT: this struct is the canonical namespace for media-
// pipeline configuration. New media-runtime knobs (subtitle format,
// transcript format, evidence-support toggles, etc.) MUST be added
// here, not as flat top-level fields on Config.
type MediaConfig struct {
	// Multilingual holds the canonical BCP-47-driven language
	// policy for the YouTube acquisition chain. The top-level
	// Config.Multilingual field is retained for back-compat; this
	// nested field is the SSOT path for new callers
	// (buildDomainMediaServices consumes cfg.Media.Multilingual.*).
	Multilingual MultilingualConfig `yaml:"multilingual"`
}

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
	Enabled      bool    `yaml:"enabled" default:"true"`
	URL          string  `yaml:"url" default:"http://localhost:8000"`
	Model        string  `yaml:"model" default:"nvidia/nemotron-nano-12b-v2-vl:free"`
	ModelVersion string  `yaml:"model_version" default:""`
	TimeoutMs    int     `yaml:"timeout_ms" default:"120000"`
	Weight       float64 `yaml:"weight" default:"0.3"`
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

// MultilingualConfig holds settings for multilingual media-language
// generation. The canonical runtime SSOT is Languages; legacy CSV
// fields remain only for compatibility with older configs.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): the
// language policy shifted from hardcoded fallbacks to a config-driven,
// BCP-47-normalized registry. godlike/06 SSOT: the canonical source
// for pipeline language capabilities is `Languages`; legacy CSV
// fields remain only for back-compat and are derived from the same
// registry in the composition root.
type MultilingualConfig struct {
	Enabled            bool     `yaml:"enabled" default:"false"`
	SourceLanguage     string   `yaml:"source_language" default:"en"`
	TranslateLanguages []string `yaml:"translate_languages" default:"it, pl, ru, de, es, pt-BR, fr, tr, en, id"`
	// IndexLanguages defines which BCP-47 language codes are included
	// in the Qdrant search_text for multilingual embedding. The
	// SearchTextBuilder concatenates transcripts/descriptions from
	// these languages so the E5 multilingual model can match queries
	// in any supported language. Default is the canonical 10-language
	// registry order.
	IndexLanguages []string `yaml:"index_languages" default:"it, en, pl, ru, de, es, pt-BR, fr, tr, id"`
	// Languages is the canonical SSOT for the pipeline's
	// language capabilities (PR-CATALOG-MULTILINGUA step 3,
	// July 2026). Loaded from `multilingual.languages:` in the
	// YAML; accepts BOTH the legacy CSV shape
	// ( `[it, en, es, ...]`, auto-promoted) AND the typed
	// struct-list shape
	// ( `[{code: it, enabled: true, translate_clips: true, generate_tts: false}, ...]` ).
	// The composition root constructs a single
	// asset.LanguageRegistry from this slice and threads it
	// into every pipeline that needs to know which languages
	// are enabled (texttracks materializer is the
	// first-migrated consumer; future steps migrate voices,
	// scripts, etc.). godlike/06 SSOT: this is the canonical
	// YAML surface for pipeline language capabilities.
	Languages LanguageSpecSlice `yaml:"languages"`

	// MaterializeLanguages is the LEGACY comma-separated
	// surface retained for back-compat with pre-step-3 YAML
	// configs. Operators are encouraged to migrate to the
	// typed `languages:` list; until they do, the composition
	// root auto-promotes this list into a registry with
	// (enabled=true, translate=true, tts=true) defaults.
	// godlike/07: if both `languages:` AND
	// `materialize_languages:` are set, the typed list wins
	// (it carries per-language flags the legacy CSV
	// cannot).
	//
	// Deprecated: use Languages (with LanguageSpec) +
	// asset.LanguageRegistry. Retained for back-compat with
	// pre-step-3 YAML configs; will be removed in step 4+
	// SSOT cutover.
	MaterializeLanguages []string `yaml:"materialize_languages" default:"it, en, pl, ru, de, es, pt-BR, fr, tr, id"`
	// RequireLanguageCertainty, when true, makes the YouTube
	// acquisition chain (TextTrackResolver.AcquireSegmentText) fail
	// with asset.ErrLanguageUndeterminable PRE-STEP-9 if no chain
	// level (1: payload, 2: DB READY, 3+4: YT subtitles, 5: Whisper)
	// surfaces a real BCP-47 language. Default false preserves the
	// pre-Fase-1.b behavior where the chain degrades to "und" silently.
	// godlike/07 fail-closed at the policy gate: when this is true
	// the writer (CommitClipTextAndIndexEvent) ALSO surfaces
	// ErrClipLocaleNotReady if a non-und language was never resolved.
	RequireLanguageCertainty bool `yaml:"require_language_certainty" default:"false"`

	// RequireTranscriptReady is the Fase 5 (PR-PY-CLIPS-CORRETTE-TRADOTTE,
	// July 2026) wire-up of the pre-existing
	// localized.CommitLocalizedClipCommand.RequireTranscriptReady
	// policy gate. When true, the YouTube segment pipeline's
	// Step 9 super-tx fails PRE-TX with
	// localized.ErrClipLocaleNotReady if no transcript-origin
	// READY track is present in the command's TextTracks.
	// Default false preserves the Fase 2.b atomic-super-tx
	// behaviour (every well-formed clip is persisted; backfill
	// is decoupled from clip-write). Operators flip to true
	// after a successful Fase 5 admin backfill pass to harden
	// the pipeline (cmd/admin/text_tracks_backfill.go).
	RequireTranscriptReady bool `yaml:"require_transcript_ready" default:"false"`

	// MigrationFallbackLegacyMetadata REMOVED in Fase 4 strict cutover (July 2026).
	// The legacy metadata_json["transcript"] / metadata_json["clean_transcript"] read is
	// RETIRED; the video pipeline reads transcripts EXCLUSIVELY from asset_text_tracks
	// via the TextTrackReader port. See
	// internal/application/scripts/usecase/clip_source_builder_transcript.go for the
	// canonical audit trail.
	// TranslationPolicy controls the application-layer model
	// selection passed to the TranslationPort for the
	// TextTrackMaterializer (Fase 3, PR-PY-CLIPS-CORRETTE-TRADOTTE,
	// July 2026). Maps onto the canonical `domain.ModelPolicy`
	// enum values:
	//   - "auto"    → server default (translation.TranslationPort
	//                 resolves the model from source/target
	//                 language pair + content length)
	//   - "fast"    → fast model (e.g. ollama gemma3:4b)
	//   - "quality" → quality model (e.g. ollama llama3:70b)
	//
	// Default "auto" — matches the pre-Fase-3 server-default
	// behaviour. Operators wanting explicit control set this to
	// "fast" or "quality" in config/multilingual.yaml.
	//
	// godlike/07 fail-closed: an invalid value is a startup
	// error (the composition root validates against the
	// domain.ModelPolicy enum at boot time, not a silent
	// fallback to "auto").
	TranslationPolicy string `yaml:"translation_policy" default:"auto"`
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
	// RetentionInterval is the periodic sweeper tick interval — controls how
	// often the job_events retention sweeper runs (when RetentionDays > 0).
	// Default 12h: balances bounded DELETE-load (lock contention) against
	// operator visibility (operator dashboards refresh 4×/day at this rate,
	// matching the qdrant-stale-cleaner cadence in pre-Wave 22 docs).
	// Accepts a duration string ("30m", "12h", "1h"); empty falls back to 12h.
	RetentionInterval string `yaml:"retention_interval" env:"VELOX_RETENTION_INTERVAL" default:"12h"`
	// RetentionSweepLimit caps the number of rows the retention sweeper
	// deletes per tick. Prevents unbounded DELETE-tx contention against
	// concurrent INSERTs on hot 100-worker deployments where single-tick
	// bursts can reach 500k+ events. Default 10000 rows/tick = ~833 rows/s
	// over 12h; sweeper catches up across multiple ticks if the delete
	// rate falls behind the insert rate. 0 disables the cap (single
	// unbounded DELETE per tick — risk of lock contention).
	RetentionSweepLimit int `yaml:"retention_sweep_limit" env:"VELOX_RETENTION_SWEEP_LIMIT" default:"10000"`
	// PR-Polling / ADR-0002 §D6.5 (June 2026): exponential-backoff
	// knobs for the server-side Worker poll loop. Three new fields
	// (all forwarded into Worker.BackoffConfig at composition time):
	//   - PollMaxBackoff is the cap on the per-poll sleep duration
	//     (exponential: pollEvery → 2× → 4× → … → PollMaxBackoff). The
	//     default 60s matches the qdrant-stale-cleaner historical
	//     cadence and bounds the worst-case Enqueue→Claim latency
	//     under sustained idle.
	//   - PollJitterFraction is the AWS-style full-jitter factor
	//     (https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/).
	//     actual_sleep = rand(0, current_backoff) per Worker per
	//     iteration; spreads thundering-herd wake-ups across the
	//     pool. 0.0 = deterministic burn of full backoff; 1.0 =
	//     uniform [0, current_backoff]. Default 0.5.
	//   - PollConsecutiveEmptyBeforeBackoff is the threshold of
	//     CONSECUTIVE empty Claims before the backoff curve
	//     escalates. Below threshold: stay at BaseInterval (=
	//     PollEvery). Above: doubles every subsequent empty claim,
	//     capped at PollMaxBackoff. Default 3; 0 disables escalation
	//     entirely (legacy fixed-poll behaviour — emergency unblock).
	// Operators alert on:
	//   rate(worker_backoff_events_total[5m]) > 0
	// for sustained periods (idle Workers accumulating backoff = the
	// queue is empty AND jobs are not being enqueued; useful BUT -
	// normally operators see this on a steady-state worker pool).
	PollMaxBackoff                    string  `yaml:"poll_max_backoff" env:"VELOX_POLL_MAX_BACKOFF" default:"60s"`
	PollJitterFraction                float64 `yaml:"poll_jitter_fraction" env:"VELOX_POLL_JITTER_FRACTION" default:"0.5"`
	PollConsecutiveEmptyBeforeBackoff int     `yaml:"poll_consecutive_empty_before_backoff" env:"VELOX_POLL_CONSECUTIVE_EMPTY_BEFORE_BACKOFF" default:"3"`

	// ProgressCoalesceWindow gates the per-jobID in-memory coalescing
	// of broker.Progress(...) calls (PR-Progress / ADR-0002 §D6.4, June 2026).
	// A worker that emits Progress(pct, msg) at >10Hz for one jobID is
	// banner-spam today: each call writes an UPDATE jobs + a separate
	// AddEvent INSERT into job_events. The coalescer buffers the latest
	// (pct, msg) per jobID and flushes 1 UPDATE + 1 INSERT per window.
	// Accepts a duration string ("100ms", "250ms", "500ms", "5s");
	// empty falls back to 100ms. 0 disables coalescing (passthrough;
	// documented as the emergency-unblock escape hatch — every
	// call writes its own row). Operators alert on
	// `rate(job_progress_events_total) / rate(job_progress_calls_total)`
	// dropping below 1.0 once coalescing is enabled: a ratio strictly
	// below 1.0 is the canonical "coalescer is reducing event pressure"
	// signal (the ratio equals calls = exact-passthrough, ratio !=
	// 0 means at least some coalescing happened, ratio = 0 means
	// every call was coalesced away by a faster-emitting same-bucket).
	// The ratio also catches misconfig: a window of 0 should report
	// ratio = 1.0 — if it doesn't the metric wiring is broken.
	ProgressCoalesceWindow string `yaml:"progress_coalesce_window" env:"VELOX_PROGRESS_COALESCE_WINDOW" default:"100ms"`
	// SearchRateLimit limits YouTube search API calls per hour for search_queries.
	// 0 = unlimited. Default 10/hour is safe for YouTube free tier (100 units/day).
	SearchRateLimit int `yaml:"search_rate_limit" default:"10"`

	// PR-Queue-Split-EXPAND / ADR-0003 §"Decider choice" PR #1 (June 2026):
	// Three knobs gate the EXPAND phase of jobs.db.sqlite split. Default
	// behavior today: every flag is OFF — jobs.db.sqlite is NOT opened;
	// *SQLiteStore reads/writes hit the jobs/job_events/dead_letter_jobs
	// tables in media.db.sqlite (unchanged). EXPAND-only; CUTOVER is a
	// future PR after the bench-driven gate in ADR-0003 §"Trigger
	// conditions" §1 lands empirical data.
	//
	// SplitDBEnabled — when true (and cfg boot wiring succeeds), the
	// composition root opens jobs.db.sqlite alongside media.db.sqlite,
	// runs migrations/sqlite_jobs/*.sql on it, and routes *SQLiteStore
	// reads/writes to the new jobs DB instead of media.db.sqlite. Default
	// OFF to keep today's production deployments unaffected.
	//
	// JobsDBPath — filesystem path for jobs.db.sqlite. When empty
	// (default), the composition root derives the path from
	// cfg.Storage.PrimaryDBFullPath() by stripping "media.db.sqlite" from
	// the basename and substituting "jobs.db.sqlite" — the canonical
	// pair lives side-by-side in <DataDir>/media/ (or whatever
	// Storage.MediaDir resolves to). Operators can override for alternate
	// layouts (e.g. a network-mounted queue DB for multi-node prep).
	// The override is a string substitution, not a remap; operators
	// who need the jobs DB on a different volume set the path explicitly.
	SplitDBEnabled bool   `yaml:"split_db_enabled" env:"VELOX_SPLIT_DB_ENABLED" default:"false"`
	JobsDBPath     string `yaml:"jobs_db_path" env:"VELOX_JOBS_DB_PATH" default:""`

	// LegacyAliasEnabled (FOR FUTURE PR-Queue-Split-LEGACY) — when true,
	// the legacy-compatibility reader (media.db.sqlite jobs tables) becomes
	// available alongside the canonical jobs.db.sqlite reads. Today this is
	// a reserved-shape knob; EXPAND itself does NOT implement the alias
	// (that lands in a future PR-Queue-Split-CUTOVER following bench results).
	// Default OFF: no legacy reads; *SQLiteStore reads are exclusive (jobs
	// DB OR media DB, never both per the gate).
	LegacyAliasEnabled bool `yaml:"legacy_alias_enabled" env:"VELOX_LEGACY_ALIAS_ENABLED" default:"false"`
	// EnableBackgroundJobs controls whether background workers/schedulers run.
	// Default true; set to false via env VELOX_ENABLE_BACKGROUND_JOBS=false for dev mode.
	EnableBackgroundJobs bool `yaml:"enable_background_jobs" env:"VELOX_ENABLE_BACKGROUND_JOBS" default:"true"`
	// EnableChannelMonitor controls the YouTube channel monitor scheduler.
	// Default false; opt-in via env VELOX_ENABLE_CHANNEL_MONITOR=true.
	EnableChannelMonitor bool `yaml:"enable_channel_monitor" env:"VELOX_ENABLE_CHANNEL_MONITOR" default:"false"`
	// EnableTestJobHandlers registers test-only job handlers (echo, slow, fail).
	// Default false; set via env VELOX_ENABLE_TEST_JOB_HANDLERS=true for dev/testing.
	EnableTestJobHandlers bool `yaml:"enable_test_job_handlers" env:"VELOX_ENABLE_TEST_JOB_HANDLERS" default:"false"`

	// PR-Deletion-Reconciler / Blocco 3.2 (June 2026): two knobs
	// gate the DeletionReconciler ticker.
	//
	// DeletionReconcilerInterval is the periodic tick that scans
	// media_assets for deletion-stuck rows + re-emits the appropriate
	// outbox event. Default 15min balances operator visibility against
	// per-tick DB scan load (the query is bounded by batchSize=100,
	// see internal/infrastructure/database/sqlite/deletion/stuck_row_scanner.go).
	//
	// DeletionReconcilerStuckThreshold is the age cutoff: rows whose
	// updated_at is older than now-threshold are eligible for
	// re-emission. Default 30min matches the Blocco 5 outbox-pool
	// backoff cap (90s × ~20 retries = 30min) — a row stuck beyond
	// this is a worker-crash or infra-fault that the pool cannot
	// self-recover from, not a transient retry storm.
	//
	// Operators alert on:
	//   rate(deletion_reconciler_actions_total[5m]) > 0  (reconciler
	//   is dispatching; expected only on recovery from bumps/crashes)
	//   AND
	//   rate(deletion_reconciler_actions_total[1h]) == 0
	// (reconciler is healthy when no actions are emitted; sustained
	// non-zero rate indicates a recurring bug).
	DeletionReconcilerInterval       string `yaml:"deletion_reconciler_interval" env:"VELOX_DELETION_RECONCILER_INTERVAL" default:"15m"`
	DeletionReconcilerStuckThreshold string `yaml:"deletion_reconciler_stuck_threshold" env:"VELOX_DELETION_RECONCILER_STUCK_THRESHOLD" default:"30m"`
}
