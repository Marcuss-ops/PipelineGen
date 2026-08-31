package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Job Metrics
	JobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_total",
		Help: "Total number of processed jobs",
	}, []string{"type", "status"})

	JobActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "jobs_active",
		Help: "Number of jobs currently in running state",
	}, []string{"type"})

	// Job Queue & Lag Metrics
	JobQueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "jobs_queue_depth",
		Help: "Number of jobs currently in the queue, partitioned by type and status",
	}, []string{"type", "status"})

	JobOldestPendingSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "jobs_oldest_pending_seconds",
		Help: "Age in seconds of the oldest queued job, by type. Zero when no job is pending.",
	}, []string{"type"})

	// Job Events Retention Metrics (PR-Retention / ADR-0002 §D6.3, June 2026)
	JobEventsCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "job_events_count",
		Help: "Row count of the job_events table AS OF THE LAST SWEEP TICK (post-DELETE COUNT). Tick-bounded: workers scrape between ticks read the prior tick's value, not live row count.",
	})

	JobEventsDeletedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "job_events_deleted_total",
		Help: "Total number of job_events rows removed by the retention sweeper (cumulative across ticks).",
	})

	JobEventsRetentionSweepDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "job_events_retention_sweep_duration_seconds",
		Help:    "Duration of a single retention sweep tick (one or more bounded DELETEs + COUNT).",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	})

	JobEventsRetentionSweepErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "job_events_retention_sweep_errors_total",
		Help: "Total number of non-fatal errors encountered during retention sweeps.",
	})

	// Job Progress Coalescing Metrics (PR-Progress / ADR-0002 §D6.4, June 2026)
	JobProgressCallsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "job_progress_calls_total",
		Help: "Total number of broker.Progress(...) calls received (including coalesce-coalesced-away ones).",
	})

	JobProgressEventsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "job_progress_events_total",
		Help: "Total number of job_events rows INSERTed by the progress coalescer.",
	})

	JobProgressCoalescedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "job_progress_coalesced_total",
		Help: "Total number of broker.Progress(...) calls buffered (overwritten) within a coalesce window.",
	})

	JobProgressFlushDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "job_progress_flush_duration_seconds",
		Help:    "Duration of a single coalescer flush operation.",
		Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25},
	})

	// Job Claim Latency (PR-Queue-Split-EXPAND / ADR-0003, June 2026)
	JobClaimDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "job_claim_duration_seconds",
		Help:    "Duration of *SQLiteStore.ClaimNext (CTE-UPDATE + job_events-INSERT + post-commit refetch).",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	})

	// Job Transition Conflict Metric (PR-F / ADR-0002 §D6.7, June 2026)
	JobTransitionConflictTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "job_transition_conflict_total",
		Help: "Total number of fenced-UPDATE failures (returned ErrTransitionConflict on revision mismatch), partitioned by method.",
	}, []string{"method"})

	// Outbox Pipeline Metrics
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

	// YouTube Pipeline Metrics
	// (PR-PY-STEP10-FAIL-LOG-OBSEVE-PARITY, July 2026)
	//
	// The Step 10 partial-state counter: incremented when the YouTube
	// process_segment.go Step 10 (metadata enrichment) fails AFTER the
	// clip write succeeded. The counter is partitioned by failure_code
	// (the stringified FailureCode constant, e.g. "metadata_failed") so
	// dashboards can aggregate partial-state events across a batch
	// extraction by failure class.
	//
	// godlike/06 SSOT (one canonical owner per fact): the
	// transcript_metadata_step10_fail_after_clip_total counter is the
	// SOLE canonical writer of "partial-state Step 10 failure" telemetry
	// in the YouTube pipeline. The typed *ExtractionError envelope with
	// FailureCodeMetadataFailed remains the canonical job-status flip
	// (the operator Warn log at PR-PY-STEP10-FAIL-LOG is preserved for
	// granular forensics; this counter is the dashboard-aggregate
	// surface that complements it per PR-PY-STEP10-FAIL-LOG-OBSEVE-PARITY).
	//
	// godlike/07 NO-FAKE-AVAILABILITY: the counter is incremented
	// exactly once per Step 10 failure, with the failure_code label
	// matching the typed error envelope's Code field. Callers MUST
	// pass the stringified FailureCode constant — the wire format
	// matches `internal/capabilities/youtube/usecase.FailureCode` so
	// dashboard queries can join against the typed-error taxonomy
	// without string parsing.
	Step10FailAfterClipTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "transcript_metadata_step10_fail_after_clip_total",
		Help: "Total number of YouTube Step 10 partial-state failures (metadata enrichment failed AFTER clip write succeeded), partitioned by failure_code. See PR-PY-STEP10-FAIL-LOG-OBSEVE-PARITY.",
	}, []string{"failure_code"})

	// Translation warnings counter is now PER-ADAPTER, registered
	// in internal/platform/observability/metrics_translation.go
	// via NewTranslationMetricsAdapter(reg). The package-level
	// global was removed in the CR#1+#2+#3 review-fix pass (per
	// godlike/06 SSOT one-canonical-owner-per-fact: the counter is
	// the metrics adapter's counter, not a package-level one). The
	// production composition root uses
	// observability.NewTranslationMetricsAdapter(prometheus.DefaultRegisterer);
	// tests use observability.NewTranslationMetricsAdapter(prometheus.NewRegistry())
	// for hermetic per-test counter assertions.
)
