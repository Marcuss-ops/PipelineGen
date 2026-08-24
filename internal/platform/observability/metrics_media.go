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

	// Media execution-plane counters. The copy-only mux (mux_audio_copy) runs
	// exactly one ffmpeg invocation with `-c:v copy -c:a copy`, so on that path
	// ffprobe_exec_count does not move and frames_decoded/frames_encoded stay 0
	// (stream copy decodes/encodes nothing). The two frame counters are
	// registered so the copy-only certification can assert a zero delta; feeding
	// them with real decoded/encoded frame counts belongs to the encode-plane
	// accounting (a separate follow-up).
	FFmpegExecCount = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ffmpeg_exec_count",
		Help: "Total number of ffmpeg invocations dispatched through the Rust media plane",
	})

	FFprobeExecCount = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ffprobe_exec_count",
		Help: "Total number of ffprobe invocations dispatched through the Rust media plane",
	})

	FramesDecoded = promauto.NewCounter(prometheus.CounterOpts{
		Name: "frames_decoded",
		Help: "Total video frames decoded by the media plane (0 on copy-only paths)",
	})

	FramesEncoded = promauto.NewCounter(prometheus.CounterOpts{
		Name: "frames_encoded",
		Help: "Total video frames encoded by the media plane (0 on copy-only paths)",
	})

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

	MediaIndexAttemptsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_attempts_total",
		Help: "Total number of asset.index.* handler entries, by event_type",
	}, []string{"event_type"})

	MediaIndexSupersededTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_superseded_total",
		Help: "Total number of asset.index.requested events short-circuited by source_version supersede, by event_type",
	}, []string{"event_type"})

	// MediaIndexSkippedTotal (PR-QDRANT-INDEXCLIP-GUARD, July 2026):
	// Total number of asset.index.requested events short-circuited
	// by the typed ErrIndexClipDisabledButEventRequested sentinel.
	// The handler stamps media_assets.index_state to
	// INDEXING_SKIPPED_NO_INDEXER and returns a retryable error so
	// the outbox pool re-emits the event when the indexer is
	// re-enabled. Distinct from MediaIndexSupersededTotal
	// (CAS-detected stale aggregate versioning — terminal, no
	// retry) and from MediaIndexRetryTotal (transient
	// network/embedding-server failures — bounded retry until
	// max_attempts).
	MediaIndexSkippedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_skipped_total",
		Help: "Total number of asset.index.requested events short-circuited because the clipindexer was disabled at runtime, by event_type",
	}, []string{"event_type"})

	MediaIndexDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "media_index_duration_seconds",
		Help:    "Duration of asset.index.* handler invocations by outcome",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"event_type", "outcome"})

	StaleAssets = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "media_index_stale_assets",
		Help: "Number of media_assets rows stuck in non-terminal index states past the freshness threshold, by source and state",
	}, []string{"source", "state"})

	EmbeddingServerLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "embedding_server_duration_seconds",
		Help:    "Duration of calls to the external embedding server, by endpoint and outcome",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"endpoint", "outcome"})

	// Media Curator Metrics
	MediaCuratorSearchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mediacurator_search_total",
		Help: "Total number of MediaCurator searches, partitioned by backend (qdrant, like, error)",
	}, []string{"backend"})

	MediaCuratorSearchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "mediacurator_search_duration_seconds",
		Help:    "Duration of MediaCurator search operations by backend",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"backend"})

	// Dedup Metrics
	DedupHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "clip_dedup_hits_total",
		Help: "Total number of clip registrations skipped because the clip was already present (dedup hit), partitioned by source and trigger",
	}, []string{"source", "trigger"})

	DedupMisses = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "clip_dedup_misses_total",
		Help: "Total number of clip registration dedup checks that found no duplicate, partitioned by source and trigger",
	}, []string{"source", "trigger"})

	DedupMerged = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "clip_dedup_merged_total",
		Help: "Total number of duplicate clips merged/soft-deleted by the post-hoc dedup sweeper",
	}, []string{"source", "reason"})

	// Deletion Reconciler Metrics (Blocco 3.2 commit 2/2, June 2026)
	//
	// The deletion-reconciler scans media_assets for rows stuck in
	// {DELETE_REQUESTED, DRIVE_DELETE_PENDING, INDEX_DELETE_PENDING}
	// past a configurable threshold (default 30min), then re-emits
	// the canonical outbox event to advance the chain. These counters
	// surface the dispatch rate per (action, from_state) combination
	// for operator dashboards.
	DeletionReconcilerActionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "deletion_reconciler_actions_total",
		Help: "Total number of deletion-reconciler dispatches by action and from_state (action=requeue_drive|requeue_index, from_state=one of 3 deletion-chain states)",
	}, []string{"action", "from_state"})

	DeletionReconcilerSkippedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "deletion_reconciler_skipped_total",
		Help: "Total number of deletion-reconciler rows skipped per tick, by reason (drift sentinel — production rows should rarely surface)",
	}, []string{"reason"})

	DeletionReconcilerErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "deletion_reconciler_errors_total",
		Help: "Total number of deletion-reconciler failures (Phase 1 SQL scan errors + per-row dispatch errors)",
	})

	DeletionReconcilerLastSuccess = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "deletion_reconciler_last_success_timestamp_seconds",
		Help: "Unix timestamp of the most recent successful deletion-reconciler tick",
	})

	DeletionReconcilerDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "deletion_reconciler_duration_seconds",
		Help:    "Duration of deletion-reconciler ticks (Phase 1 + Phase 2 + Phase 3 + Phase 4)",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	})

	// ── Projection Reconciler Metrics (plan item #15, August 2026) ──
	//
	// The periodic projection-reconciler compares the canonical eligible
	// SQLite asset set (SearchIndexEligibilitySQL SSOT) against the
	// asset_ids present in the ACTIVE Qdrant projection and emits the
	// parity below every tick. Target steady state:
	//
	//	projection_coverage_ratio == 1.0  (every eligible asset is in Qdrant)
	//	projection_orphan_count     == 0   (no stale points)
	//
	// Alert on: projection_coverage_ratio < 1.0 sustained, or
	// projection_orphan_count > 0 (reconcile-qdrant / reindex-qdrant
	// repair needed), or rate(projection_reconcile_errors_total[15m]) > 0.
	ProjectionCoverageRatio = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "projection_coverage_ratio",
		Help: "Fraction of eligible SQLite assets present in the active Qdrant projection (1.0 = full coverage; eligible-present / eligible)",
	})

	ProjectionOrphanCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "projection_orphan_count",
		Help: "Number of points in the active Qdrant projection whose asset_id has no eligible SQLite row (stale projection points)",
	})

	ProjectionMissingCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "projection_missing_count",
		Help: "Number of eligible SQLite assets absent from the active Qdrant projection (projection lag / missing points)",
	})

	ProjectionEligibleSQLite = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "projection_eligible_sqlite",
		Help: "Number of media_assets rows satisfying the canonical eligibility policy (SearchIndexEligibilitySQL SSOT)",
	})

	ProjectionQdrantPoints = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "projection_qdrant_points",
		Help: "Number of points in the active Qdrant projection carrying a payload asset_id",
	})

	ProjectionScanComplete = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "projection_scan_complete",
		Help: "1 when the last projection parity scan completed cleanly (every point scanned, zero errors), 0 otherwise",
	})

	ProjectionReconcileRunsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "projection_reconcile_runs_total",
		Help: "Total number of successful projection-reconciler ticks (parity sampled)",
	})

	ProjectionReconcileErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "projection_reconcile_errors_total",
		Help: "Total number of projection-reconciler tick failures (parity check errored)",
	})

	ProjectionReconcileLastSuccess = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "projection_reconcile_last_success_timestamp_seconds",
		Help: "Unix timestamp of the most recent successful projection-reconciler tick",
	})

	// ── Finalizer Spine Metrics (PR-FINALIZER-METRICS, July 2026) ─
	//
	// Three counters capture the canonical per-event outcome of the
	// JobFinalizer.CompleteWithArtifacts spine (the SINGLE writer of
	// terminal SUCCEEDED per godlike/06 SSOT). Together they let
	// operators alert on gate-filter drift without grepping stderr.
	//
	// Convention (per AGENTS.md observability + this file's existing
	// metrics): CamelCase Go identifier, snake_case prom Name,
	// lowercase snake_case label values.
	//
	// Bump-once-per-event semantics: each counter increments exactly
	// once per outcome, NOT once per retry. Retry-induced duplicate
	// events are routed through `idempotency` counters in the
	// jobs-complete lifecycle, NOT these per-outcome counters.

	// FinalizerMediaAssetsInsertTotal — bumped once per
	// upsertMediaAsset result, mapping the SQLite rows-affected
	// outcome into a 5-state classifier:
	//   * insert             — rows_affected=1 (new row written)
	//   * update_on_conflict — rows_affected=2 (ON CONFLICT DO UPDATE fired)
	//   * no_op_silent       — rows_affected=0 (idempotent-retry, no diff)
	//   * rows_affected_err  — RowsAffected() itself returned non-nil (driver-divergence)
	//   * failed             — tx.ExecContext returned non-nil error before rows-affected
	FinalizerMediaAssetsInsertTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "finalizer_media_assets_insert_total",
		Help: "Total number of media_assets UPSERTs attempted by the JobFinalizer spine, by SQLite rows-affected outcome",
	}, []string{"outcome"})

	// FinalizerWriteArtifactsIterTotal — bumped once per-iteration
	// (per artifact) inside writeArtifacts. NOT per-call: 1 call
	// iterates over N artifact slots and produces N increments.
	// outcome=ok when FinalizeAsset returned (ArtifactRef, events, nil);
	// outcome=err when FinalizeAsset returned a non-nil error (which
	// causes writeArtifacts to early-return with the wrapped error).
	FinalizerWriteArtifactsIterTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "finalizer_write_artifacts_iter_total",
		Help: "Total number of FinalizeAsset iterations invoked from writeArtifacts, by per-iteration outcome (ok=FinalizeAsset succeeded, err=FinalizeAsset returned non-nil)",
	}, []string{"outcome"})

	// FinalizerCompleteArtifactsTotal — bumped exactly once per
	// CompleteWithArtifacts call (NOT per artifact). outcome=ok when
	// the named-return err was nil at defer-time; outcome=err when
	// any of the 8 typed-error sentinels fired (validation / begin_tx
	// / lease_fence / idempotent / write_artifacts / write_outbox /
	// mark_succeeded / commit). Per-error-class cardinality is
	// intentionally NOT surface in this counter; future per-class
	// metrics should be added in a follow-up wave if alert precision
	// requires it.
	FinalizerCompleteArtifactsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "finalizer_complete_artifacts_total",
		Help: "Total number of JobFinalizer.CompleteWithArtifacts calls, by terminal outcome (ok=spine returned nil error, err=any of the 8 typed-error sentinels fired)",
	}, []string{"outcome"})
)

// Note (Blocco 3.2 commit 2/2 — package boundary)
// ─────────────────────────────────────────────────
// The deletion_reconciler_* Prometheus counters declared above
// (DeletionReconcilerActionsTotal / SkippedTotal / ErrorsTotal /
// LastSuccess / Duration) are consumed via the canonical Pattern 0
// adapter at
//
//	internal/infrastructure/database/sqlite/deletion/metrics_adapter.go
//
// type ReconcilerMetricsAdapter struct{}
//
// which exposes them against the application-layer
// reconciler.Metrics port. The composition root wires
// `deletion.ReconcilerMetricsAdapter{}` into
// reconciler.NewServiceFromDeps — see internal/app/lifecycle.go.
//
// Earlier iterations of this commit placed an unexported
// `deletionReconcilerMetricsAdapter` directly in this file; that
// shape conflicted with the canonical deletion-package adapter
// (two concretes satisfying the same port = Pattern 0 violation).
// The unexported adapter has been removed in favour of the
// deletion-package adapter as the single owner of the port.
