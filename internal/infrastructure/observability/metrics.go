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
	//   generate            — Engine.Generate (memory gate + LLM)
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

	// ── Job Events Retention Metrics (PR-Retention / ADR-0002 §D6.3, June 2026) ──
	// Gauge semantics (PRD-Retention item #2 from the code-review pass):
	//   JobEventsCount is the canonical "what's currently in job_events
	//   row count AS OF THE LAST SWEEP TICK" gauge. Updated ONLY at end of
	//   tick (post-DELETE COUNT). Operators scraping /metrics between
	//   ticks will read the LAST tick's value even though the table may
	//   have accumulated N=K_inserts×Δt recent rows since then. This is
	//   the canonical "tick-bounded" semantics; the alternative (live
	//   count) would require bumping the gauge from every AddEvent /
	//   Complete / Fail / Retry / Reaper / Cancel / ScheduleRetry /
	//   SetProgress / ClaimNext event-write path, which adds a hot-path
	//   dependency for marginal operator benefit. Document the tick-
	//   bounded nature explicitly so dashboards and alerting rules read
	//   it as expected: read the gauge AT TICK boundaries; the value
	//   reflects "rows younger than cutoff" by construction.
	//   Operators alert on monotonically-growing tick readings (sweeper
	//   falling behind) or weekly-step jumps (sudden DELETE burst =
	//   possible threshold misconfig via VELOX_RETENTION_DAYS).
	//
	// Naming follows the AGENTS.md Pattern 0 convention:
	//   no `_total` suffix on gauges; counters ALWAYS carry `_total`.
	JobEventsCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "job_events_count",
		Help: "Row count of the job_events table AS OF THE LAST SWEEP TICK (post-DELETE COUNT). Tick-bounded: workers scrape between ticks read the prior tick's value, not live row count. Stabilises below the N-day watermark once the sweeper keeps pace with the insert rate. Operators alert on monotonic growth across ticks (sweeper falling behind) or weekly-step jumps (DELETE burst = threshold misconfig).",
	})

	// JobEventsDeletedTotal counts rows removed by the retention sweeper.
	// Increments on every successful per-chunk DELETE inside the sweeper
	// Tick (cumulative rate-of-removal across the process lifetime).
	// Per-process: resets to 0 on every restart. The canonical operator
	// signal is the DELTA per tick (read at tick boundaries via
	// `rate()` over 12h-aligned windows matching RetentionInterval).
	JobEventsDeletedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "job_events_deleted_total",
		Help: "Total number of job_events rows removed by the retention sweeper (cumulative across ticks). Use rate() aligned to RetentionInterval to spot sweeper pathology.",
	})

	// JobEventsRetentionSweepDuration measures wall-clock duration of a
	// single retention sweep tick (one or more bounded DELETE chunks +
	// COUNT, all in a single Tick call). Buckets are sized for the
	// typical 10ms-30s envelope with a 300s worst-case bucket for
	// pathological 10k-row sweeps on a hot DB.
	JobEventsRetentionSweepDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "job_events_retention_sweep_duration_seconds",
		Help:    "Duration of a single retention sweep tick (one or more bounded DELETEs + COUNT). Buckets sized for typical 10ms-30s envelope + 300s worst-case.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	})

	// JobEventsRetentionSweepErrorsTotal counts non-fatal per-tick errors
	// (e.g. one DELETE chunk failed mid-sweep). The sweep overall may still
	// succeed; this counter is the input for "sweeper unhealthy" alerts.
	JobEventsRetentionSweepErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "job_events_retention_sweep_errors_total",
		Help: "Total number of non-fatal errors encountered during retention sweeps. Non-zero rate means the sweeper is missing ticks.",
	})

	// ── Job Progress Coalescing Metrics (PR-Progress / ADR-0002 §D6.4, June 2026) ──
	// Operators alert on the ratio
	//   rate(job_progress_events_total[5m]) / rate(job_progress_calls_total[5m])
	// dropping below 1.0 once coalescing is enabled: a ratio strictly
	// below 1.0 is the canonical "coalescer is reducing event pressure"
	// signal (= N − coalesced calls/users N calls). A ratio of 0 means
	// every call coalesced away (impossible unless the window is also
	// 0, which would be a metric-wiring regression). Operators also
	// alert on rate(job_progress_flush_duration_seconds_sum) /
	// rate(job_progress_flush_duration_seconds_count) exceeding p99
	// since flush latency directly competes with broker throughput.

	// JobProgressCallsTotal counts every broker.Progress(...) call
	// received from a worker (or via admin/handler paths routed through
	// the coalescer). Includes both coalesce-coalesced-away calls AND
	// the eventual winner-pushed-through-to-SQL calls. Stable across
	// window changes; resets to 0 on process restart.
	JobProgressCallsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "job_progress_calls_total",
		Help: "Total number of broker.Progress(...) calls received (including coalesce-coalesced-away ones). The numerator of the coalesce-ratio (calls / events).",
	})

	// JobProgressEventsTotal counts every actual `job_events` row
	// INSERT that the coalescer flushed to disk (1 per coalesce-window
	// per jobID when coalescing is active; 1 per call when disabled).
	// The denominator of the coalesce-ratio. Operators alert on
	//   events < calls - coalesced
	// (invariant: calls == events + coalesced) — drift between the
	// three counters indicates a wiring bug.
	JobProgressEventsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "job_progress_events_total",
		Help: "Total number of job_events rows INSERTed by the progress coalescer (1 per coalesce window per jobID).",
	})

	// JobProgressCoalescedTotal counts every broker.Progress(...) call
	// that was buffered (and replaced by a newer call) within a coalesce
	// window. Resets to 0 on process restart. The canonical
	// "coalescer is reducing event pressure" signal: a steady-state
	// rate > 0 with events < calls indicates healthy coalescing.
	JobProgressCoalescedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "job_progress_coalesced_total",
		Help: "Total number of broker.Progress(...) calls buffered (overwritten) within a coalesce window. The signal for \"coalescer is reducing event pressure\".",
	})

	// JobProgressFlushDuration measures wall-clock duration of a single
	// coalescer flush op (1 update + 1 insert per pending bucket; bounded
	// by the per-call mu). Buckets sized for the typical 0.1ms-10ms
	// envelope plus a 250ms worst-case for hot DB contention.
	JobProgressFlushDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "job_progress_flush_duration_seconds",
		Help:    "Duration of a single coalescer flush operation (1 UPDATE + 1 INSERT per pending bucket). Buckets sized for typical 0.1ms-10ms envelope + 250ms worst-case for hot DB contention.",
		Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25},
	})

	// ── Worker Polling Backoff Metrics (PR-Polling / ADR-0002 §D6.5, June 2026) ──
	// Operators alert on:
	//   - rate(worker_idle_ticks_total[5m]) > 0 for sustained periods;
	//     paired with rate(worker_backoff_events_total[5m]) > 0 ⇒
	//     "Workers saturating the backoff curve" = queue is empty
	//     AND jobs are not being enqueued (steady-state OK).
	//   - rate(worker_wake_on_enqueue_total[5m]) > 0 ⇒ Enqueue is
	//     arriving faster than the natural poll cadence; the
	//     wake-on-broadcast primitive is the canonical reason.

	// WorkerIdleTicksTotal counts every Poll-loop iteration where
	// ClaimNext returned (nil, nil) (the queue was empty). The
	// counter increments regardless of whether the polling is at
	// BaseInterval or in the backoff curve.
	WorkerIdleTicksTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "worker_idle_ticks_total",
		Help: "Total number of worker poll-loop iterations that returned an empty ClaimNext (queue empty). Resets to 0 on process restart.",
	})

	// WorkerBackoffEventsTotal counts every escalation of the
	// per-worker backoff curve (currentBackoff doubled successfully,
	// capped at MaxBackoff). A monotonic rise during the day is
	// normal (idle workers accumulate backoff over time). A
	// spike-to-zero dynamic indicates the queue refilled and
	// workers reset via the
	// "successful claim → reset backoff" path.
	WorkerBackoffEventsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "worker_backoff_events_total",
		Help: "Total number of worker backoff escalations (currentBackoff doubled, capped at MaxBackoff). Resets to 0 on process restart.",
	})

	// WorkerWakeOnEnqueueTotal counts every Worker poll-iteration
	// that was terminated early by the QueueNotifier wake broadcast
	// (Enqueue / Retry / RequeueExpiredLeases trigger).  Paired
	// with WorkerIdleTicksTotal: high wake + high idle = "Enqueue
	// rate exceeds poll cadence; backoff is matching the floor";
	// operators use this to right-size MaxBackoff vs Enqueue rate.
	WorkerWakeOnEnqueueTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "worker_wake_on_enqueue_total",
		Help: "Total number of worker poll iterations terminated early by the QueueNotifier wake broadcast (Enqueue / Retry / RequeueExpiredLeases).",
	})

	// ── Job Claim Latency (PR-Queue-Split-EXPAND / ADR-0003, June 2026) ──
	// JobClaimDurationSeconds is the canonical ClaimNext latency
	// histogram. ADR-0003 §"Trigger conditions" §1 references this metric
	// to detect WAL contention: "A 7-day rolling window of
	// JobClaimDurationSeconds{p99} > 100ms observed under non-degenerate
	// queue write pressure (≥10 enqueue/s sustained)" is the bench-driven
	// re-evaluation trigger (Option C landing condition). Operators alert
	// on p99 > 100ms as the canonical "jobs.db.sqlite split is now
	// warranted" signal.
	//
	// Buckets are sized for the typical 0.1–50 ms envelope (steady-state
	// CTE atomic claim on a quiet DB) with a 5 s tail bucket for
	// pathological hot-DB contention. The 0.1 s bucket matches the trigger
	// condition's 100 ms threshold exactly — p99-quantile frontier lives
	// at this bucket boundary in steady state.
	//
	// Observability is ALWAYS-ON (not gated behind cfg.Jobs.SplitDBEnabled) —
	// ADR-0003 §"Trigger conditions" §1 needs the histogram emitted
	// regardless of split-mode so the bench (when it lands) can use it
	// identically. Histogram cost is negligible (1 atomic per call).
	//
	// Observed at the storage layer (*SQLiteStore.ClaimNext returns from
	// commit) — captures the END-TO-END claim path (CTE-UPDATE +
	// job_events-INSERT + post-commit Get refetch) inside the same tx.
	// The histogram is the canonical "what is the claim path costing us
	// today" surface for both single-DB (today) and split-DB EXPAND
	// (forward). Operators dashboard the ratio p99 / base-interval to
	// spot WAL-write contention ahead of bench reconfirming it.
	JobClaimDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "job_claim_duration_seconds",
		Help:    "Duration of *SQLiteStore.ClaimNext (CTE-UPDATE + job_events-INSERT + post-commit refetch). ADR-0003 §1 trigger condition: p99 > 100ms under sustained queue write pressure indicates WAL contention; jobs.db.sqlite split is warranted when this fires.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	})

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

	// ── Zero-Legacy §07 deprecation metrics (PR 9, June 2026) ───────────────
	// Monotonic counters that drive the §07 removal-deadline gate for two
	// long-standing backwards-compat surfaces. Both surfaces are owned by
	// internal/application/scripts/.
	//
	// Naming note: the spec asked for `_per_day` suffix; the canonical
	// Prometheus convention here is `_total` for monotonic counters
	// (promtool/promlint reject `_per_day`, and Overrides/Dashboards
	// assume `_total` for rate()). Operators derive the per-day rate
	// via standard PromQL:
	//
	//   increase(curate_legacy_invocations_total[1d])
	//   increase(legacy_array_to_output_invocations_total[1d])
	//
	// The §07 records reference both names; the spec's `_per_day`
	// derivation is a PromQL `increase(x_total[1d])` away.
	//
	// Removal gate (per docs/architecture/godlike/14 §18):
	//   curate_legacy_invocations_total        == 0 for 30 consecutive days
	//   legacy_array_to_output_invocations_total == 0 for 60 consecutive days
	//
	// `source` label cardinality is bounded by the static set of callers —
	// values are documented on each CounterVec below. Prefixing unknown /
	// new sources is a v2 review (architecturally: expanding the label
	// cardinality requires a Zero-Legacy §07 doc update FIRST).

	// CurateLegacyInvocations counts every call to the deprecated
	// MediaCurator.Curate entry point (legacy /curate HTTP route through
	// internal/api/script/handler_legacy_adapters.go::LegacyCurate).
	// Pre-PR-4 callers routed curate requests here; Post-PR-4 every
	// curate source goes through SourceRegistry → GenerateOneUseCase.
	//
	// Source label values (bounded): "youtube" | "artlist" | "local" |
	// "stock" | "unknown" (catch-all when req.Source is empty).
	CurateLegacyInvocationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "curate_legacy_invocations_total",
		Help: "Monotonic counter for the deprecated MediaCurator.Curate entry point (DL-CURATIONTYPES-001). Spec name: curate_legacy_invocations_per_day — derive via increase(...[1d]). Removal gate: 0 for 30 consecutive days. Source label: provider/source string, bounded by static caller set.",
	}, []string{"source"})

	// LegacyArrayToOutputInvocations counts every successful invocation
	// of compat.LegacyArrayToOutput inside Engine.decodeModelPayload
	// (the legacy array-shape fallback for pre-V1 cache rows). New cache
	// writes MUST emit canonical V1; this counter should trend to 0
	// once all pre-V1 cache entries are evicted.
	//
	// Source label values (bounded): "cache" (memory-gate cache hit
	// path — only path where legacy is legitimately expected) |
	// "fresh" (ollama direct decode — should ALWAYS be zero; non-zero
	// indicates a V1 contract regression on the model side).
	LegacyArrayToOutputInvocationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "legacy_array_to_output_invocations_total",
		Help: "Monotonic counter for compat.LegacyArrayToOutput invocations (DL-COMPAT-LEGACYDECODER-001). Spec name: legacy_array_to_output_invocations_per_day — derive via increase(...[1d]). Removal gate: 0 for 60 consecutive days. Source label: 'cache' for memory-gate replay path (legitimate pre-V1 rows); 'fresh' for ollama-direct path (must be zero in steady state; non-zero indicates V1 contract regression).",
	}, []string{"source"})
)
