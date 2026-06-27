package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Video Render Metrics
	VideoRenderDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "video_render_duration_seconds",
		Help:    "Duration of video rendering jobs",
		Buckets: prometheus.DefBuckets,
	}, []string{"status", "fallback"})

	VideoRenderTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "video_render_total",
		Help: "Total number of video rendering attempts",
	}, []string{"status", "fallback"})

	// Download Metrics
	DownloadDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "download_duration_seconds",
		Help:    "Duration of media downloads",
		Buckets: prometheus.DefBuckets,
	}, []string{"source", "status"})

	DownloadTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "download_total",
		Help: "Total number of media downloads",
	}, []string{"source", "status"})

	// Job Metrics
	JobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_total",
		Help: "Total number of processed jobs",
	}, []string{"type", "status"})

	JobActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "jobs_active",
		Help: "Number of jobs currently in running state",
	}, []string{"type"})

	// Qdrant Vector Store Metrics
	QdrantSearchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "qdrant_search_duration_seconds",
		Help:    "Duration of Qdrant vector search operations",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"vector_name", "status"})

	QdrantSearchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_search_total",
		Help: "Total number of Qdrant vector search operations",
	}, []string{"vector_name", "status"})

	QdrantUpsertTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_upsert_total",
		Help: "Total number of Qdrant upsert operations",
	}, []string{"status"})

	QdrantCollectionSize = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "qdrant_collection_size",
		Help: "Number of points in the Qdrant collection",
	}, []string{"collection"})

	QdrantHealthStatus = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qdrant_health_status",
		Help: "Qdrant health status: 1 = healthy, 0 = unreachable",
	})

	QdrantErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_errors_total",
		Help: "Total number of Qdrant operation errors",
	}, []string{"operation"})

	// Script Generation Metrics
	ScriptGenerationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "script_generation_duration_seconds",
		Help:    "Duration of script generation calls (Ollama round-trip)",
		Buckets: []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"model", "language", "outcome"})

	ScriptGenerationTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "script_generation_total",
		Help: "Total number of script generation attempts",
	}, []string{"model", "language", "outcome"})

	ScriptCacheHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "script_cache_hits_total",
		Help: "Memory gate cache hits, partitioned by level: exact (returned the old output), reference (injected avoid-list to nudge a fresh variant), fresh (no prior memory, generation was clean)",
	}, []string{"level", "channel_id"})

	ScriptMemoryEntries = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "script_memory_entries",
		Help: "Current row count of gemmamemory tables, by table",
	}, []string{"table"})

	ScriptNearDuplicates = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "script_near_duplicates_total",
		Help: "Generations flagged as near-duplicate of a prior run by DetectNearDuplicate (n-gram Jaccard >= threshold)",
	}, []string{"channel_id"})

	// ── Phase-level Timing Metrics ──────────────────────────────────────
	// Each histogram measures a single phase of the script generation
	// pipeline, with the "phase" label identifying the sub-operation.
	// This lets you query "which phase is slowest right now" at a glance.
	//
	// Common phase values:
	//   total_request       — wall-clock from handler entry to response
	//   write_script        — Engine.WriteScript (memory gate + LLM + normalize + persist)
	//   validation          — post-generation ValidateScript
	//   entity_extraction   — LLM entity extraction
	//   insight_building    — buildGeneratedScriptInsights (image search, clip search, drive recommendations)
	//   video_metadata      — GenerateVideoMetadata (LLM + translations)
	//   google_doc          — maybeCreateGoogleDoc (Drive API call)
	//   db_enrich           — saveTextEnrichedMetadata
	ScriptPhaseDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "script_phase_duration_seconds",
		Help:    "Duration of each phase in the script generation pipeline",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"phase", "topic"})

	ScriptPhaseTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "script_phase_total",
		Help: "Total number of script phase executions",
	}, []string{"phase", "topic"})

	// Media Index Pipeline Metrics
	MediaIndexSuccessTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_success_total",
		Help: "Total number of successfully indexed media assets, by source",
	}, []string{"source"})

	MediaIndexFailureTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_failure_total",
		Help: "Total number of failed media indexing attempts, by source",
	}, []string{"source"})

	MediaIndexRetryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_retry_total",
		Help: "Total number of media indexing retries, by source",
	}, []string{"source"})

	// MediaIndexAttemptsTotal counts every handler entry into the
	// asset.index.* event consumer (IndexingHandler). Distinct from
	// RetryTotal (which counts retries specifically) and SuccessTotal /
	// FailureTotal (which count terminal outcomes): the attempts counter
	// is the sum of in-flight + retrying + succeeded + failed. Stable
	// high attempts with low success_total indicates an embedding-server
	// or Qdrant bottleneck — alert thresholds draw on the delta.
	//
	// QDRANT-002 item M: "media_index_attempts_total".
	MediaIndexAttemptsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_attempts_total",
		Help: "Total number of asset.index.* handler entries, by event_type",
	}, []string{"event_type"})

	// MediaIndexSupersededTotal counts events short-circuited by the
	// source_version supersede gate (see outboxevents.SupersedeError).
	// Steady-state growth during Drive sync waves is normal (catalogsync
	// streams hundreds of updates for the same aggregate in a burst).
	// Out-of-band growth indicates an upstream regression — same
	// aggregate re-streamed unnecessarily.
	//
	// QDRANT-002 item M: success-like terminal state surfaced alongside
	// dead_letter so dashboards tell "producer broken" apart from
	// "upstream streamed a fresh update — old events no-op".
	MediaIndexSupersededTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_superseded_total",
		Help: "Total number of asset.index.requested events short-circuited by source_version supersede, by event_type",
	}, []string{"event_type"})

	// MediaIndexDuration measures wall-clock duration of the asset.index.*
	// handler (IndexingHandler.Handle), bucketed for the typical
	// 30s process timeout window plus a 120s tail bucket for embedding
	// worst-case. Pairs naturally with attempts_total + success_total to
	// spot slow processing.
	//
	// outcome label values:
	//   - "success"   — IndexClip returned nil, MarkCompleted path
	//   - "superseded"— source_version mismatch, MarkSuperseded path
	//   - "terminal"  — handler returned *TerminalError, MarkDeadLetter
	//   - "retryable" — handler returned retryable error, MarkFailed path
	//   - "parse_err" — payload malformed / schema mismatch / missing fields
	//
	// QDRANT-002 item M: "media_index_duration_seconds".
	MediaIndexDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "media_index_duration_seconds",
		Help:    "Duration of asset.index.* handler invocations by outcome",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"event_type", "outcome"})

	// StaleAssets counts media_assets rows in non-terminal indexing states
	// beyond a freshness threshold (default 1h). Updated by the indexer
	// health sweeper — alert if it grows monotonically.
	StaleAssets = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "media_index_stale_assets",
		Help: "Number of media_assets rows stuck in non-terminal index states past the freshness threshold, by source and state",
	}, []string{"source", "state"})

	// EmbeddingServerLatency tracks the round-trip cost of hitting the
	// external embedding server (/index, /index_transcript, /index_bulk).
	// High latency here directly slows the clipindexer pipeline.
	EmbeddingServerLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "embedding_server_duration_seconds",
		Help:    "Duration of calls to the external embedding server, by endpoint and outcome",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"endpoint", "outcome"})

	// Job Queue & Lag Metrics — expose what's waiting in the queue.
	JobQueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "jobs_queue_depth",
		Help: "Number of jobs currently in the queue, partitioned by type and status",
	}, []string{"type", "status"})

	JobOldestPendingSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "jobs_oldest_pending_seconds",
		Help: "Age in seconds of the oldest queued job, by type. Zero when no job is pending.",
	}, []string{"type"})

	// Outbox Pipeline Metrics — Qdrant indexing queue depth and lag.
	OutboxQueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "outbox_queue_depth",
		Help: "Current number of outbox entries by status (pending, in_flight, processed, dead_letter)",
	}, []string{"status"})

	OutboxOldestPendingSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "outbox_oldest_pending_seconds",
		Help: "Age in seconds of the oldest pending outbox entry (Qdrant indexing lag)",
	})

	OutboxProcessingDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "outbox_processing_duration_seconds",
		Help:    "Duration of outbox entry processing (claim to complete)",
		Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"status"})

	// Qdrant Stale Cleaner Metrics
	QdrantStaleTombstoned = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qdrant_stale_tombstoned",
		Help: "Number of Qdrant points tombstoned (grace period started) in last cleanup run",
	})

	QdrantStaleDeleted = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qdrant_stale_deleted",
		Help: "Number of Qdrant points hard-deleted (grace period expired) in last cleanup run",
	})

	// SearchNoResultsTotal counts search queries that returned zero hits,
	// by vector name. Use to detect empty-index or missing-asset regressions.
	SearchNoResultsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "search_no_results_total",
		Help: "Total number of vector searches that returned zero results, by vector name",
	}, []string{"vector_name"})

	// Dedup Metrics
	// Tracks clip-dedup outcomes by source and trigger (pre-check vs sweeper).
	DedupHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "clip_dedup_hits_total",
		Help: "Total number of clip registrations skipped because the clip was already present (dedup hit), partitioned by source and trigger",
	}, []string{"source", "trigger"})

	DedupMisses = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "clip_dedup_misses_total",
		Help: "Total number of clip registration dedup checks that found no duplicate (proceeding with creation), partitioned by source and trigger",
	}, []string{"source", "trigger"})

	DedupMerged = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "clip_dedup_merged_total",
		Help: "Total number of duplicate clips merged/soft-deleted by the post-hoc dedup sweeper",
	}, []string{"source", "reason"})

	// Channel Monitor Metrics
	ChannelMonitorVideosChecked = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "channel_monitor_videos_checked_total",
		Help: "Total number of videos checked by the channel monitor, by channel",
	}, []string{"channel"})

	ChannelMonitorVideosWithSegments = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "channel_monitor_videos_with_segments_total",
		Help: "Videos where at least one segment was found, by channel",
	}, []string{"channel"})

	ChannelMonitorSegmentsFound = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "channel_monitor_segments_found_total",
		Help: "Total number of segments found by Gemma, by channel",
	}, []string{"channel"})

	ChannelMonitorSegmentsPerVideo = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "channel_monitor_segments_per_video",
		Help:    "Distribution of segments found per video by channel",
		Buckets: []float64{0, 1, 2, 3, 4, 5, 6, 8, 10},
	}, []string{"channel"})

	// Media Curator Metrics
	// Tracks search backend usage: "qdrant" when Qdrant hybrid search succeeds,
	// "like" when the SQLite LIKE fallback is used, "error" when all backends fail.
	MediaCuratorSearchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mediacurator_search_total",
		Help: "Total number of MediaCurator searches, partitioned by backend (qdrant, like, error)",
	}, []string{"backend"})

	MediaCuratorSearchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "mediacurator_search_duration_seconds",
		Help:    "Duration of MediaCurator search operations by backend",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"backend"})

	// ── QDRANT-005C Observability ─────────────────────────────────────
	// Reconciler & legacy-cleanup metrics. Names follow the
	// Prometheus naming convention:
	//   _total               — Counter (monotonic)
	//   _seconds             — Histogram (duration)
	//   _timestamp_seconds   — Gauge (Unix epoch of last successful event)
	//
	// ReconcileRunMode label values: "dry_run" | "apply".

	// ReconcilerLastSuccess is the Unix timestamp of the most recent
	// successful Reconcile run (regardless of DryRun / Apply). Updated
	// by Service.Reconcile at end-of-run. Allows ops dashboards to
	// alert on staleness ("no successful reconcile in N minutes").
	ReconcilerLastSuccess = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qdrant_reconciler_last_success_timestamp_seconds",
		Help: "Unix timestamp of the most recent successful reconciler run (DryRun or Apply).",
	})

	// ReconcilerDuration measures wall-clock duration of a Reconcile
	// run by mode (dry_run / apply). Buckets sized for the typical
	// 5-300s run envelope; the tail bucket (max=300s) covers worst-case
	// 200k-point scrolls.
	ReconcilerDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "qdrant_reconciler_duration_seconds",
		Help:    "Duration of reconciler runs by mode (dry_run, apply).",
		Buckets: []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"mode"})

	// ReconcilerFindingsTotal counts every classification emitted by
	// the scanner, partitioned by ClassificationKind (9 label values).
	// Reported on BOTH DryRun and Apply (drift visibility is the
	// primary value — repair is the secondary outcome).
	ReconcilerFindingsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_reconciler_findings_total",
		Help: "Total number of classification findings emitted by the reconciler, by kind (9 categories).",
	}, []string{"kind"})

	// ReconcilerErrorsTotal counts non-fatal per-run errors (e.g. one
	// scroll page failed). The run overall may still succeed; this
	// counter is the input for "reconcile is unhealthy" alerts.
	ReconcilerErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "qdrant_reconciler_errors_total",
		Help: "Total number of non-fatal errors encountered during reconciler runs.",
	})

	// ReconcilerVersionMismatchPerChannel breaks down version-stale
	// findings by embedding channel (text, transcript, visual, audio).
	// Useful for spotting "the visual model regressed this week but
	// text still matches" — alerts on the channel with the largest
	// delta vs. baseline.
	ReconcilerVersionMismatchPerChannel = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_reconciler_version_mismatch_per_channel_total",
		Help: "Total number of version-stale classifications, by embedding channel.",
	}, []string{"channel"})

	// ReconcilerDispatchesTotal counts repair actions actually fired,
	// by action label: "reindex" | "delete" | "payload_strip". Apply
	// mode only. DryRun emits ZERO so dashboards distinguish "scan ran"
	// from "repairs ran".
	ReconcilerDispatchesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_reconciler_dispatches_total",
		Help: "Total number of repair actions dispatched by the reconciler, by action (reindex, delete, payload_strip). Apply mode only.",
	}, []string{"action"})

	// PayloadLegacyCleanedTotal counts points stripped of legacy
	// payload keys by the reconciler, partitioned by key name
	// (status / drive_link / local_path). Apply mode only.
	PayloadLegacyCleanedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_payload_legacy_cleaned_total",
		Help: "Total number of legacy payload keys stripped from Qdrant points by the reconciler, by key.",
	}, []string{"legacy_key"})

	// ── QDRANT-005C DR/snapshot alias-switch telemetry ────────────────
	// Forward-looking placeholders for PR3 (DR/snapshots) wiring.
	// Declared now (QDRANT-005C scope per user spec) so dashboards and
	// alerts can be configured against stable metric names regardless
	// of the wire-up order — production wiring lands in PR3 alongside
	// the SnapshotService / RestoreService that produce these signals.
	// Until then, the counters stay at 0 and the gauge stays at the
	// initialised runtime alias target.

	// QdrantAliasSwitchTotal counts every alias-switch operation,
	// partitioned by action label: "switch" (active alias swapped to
	// restore candidate), "rollback" (active alias restored from
	// rotate-back snapshot), "rehydrate" (alias re-bound after the
	// primary collection was rewritten).
	QdrantAliasSwitchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_alias_switch_total",
		Help: "Total number of alias-switch operations, by action (switch, rollback, rehydrate).",
	}, []string{"action"})

	// QdrantAliasSwitchDuration measures wall-clock duration of
	// alias-switch operations by action. Buckets sized for typical
	// 10ms-5s Qdrant REST round-trips.
	QdrantAliasSwitchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "qdrant_alias_switch_duration_seconds",
		Help:    "Duration of alias-switch operations by action (switch, rollback, rehydrate).",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"action"})

	// QdrantAliasCurrentCollection exposes the current physical
	// collection pointing at each runtime alias (e.g.
	// `media_assets_current` -> `media_assets_v3_...`). Updates on
	// every successful alias switch. Operators can alert on
	// `qdrant_alias_current_collection{alias=...}` matching a planned
	// target to verify a DR switch actually landed.
	QdrantAliasCurrentCollection = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "qdrant_alias_current_collection",
		Help: "Current physical collection bound to each runtime alias. Set to 1 for the current target, 0 otherwise (PromQL: alias != '...' filters out unbound aliases).",
	}, []string{"alias", "collection"})
)
