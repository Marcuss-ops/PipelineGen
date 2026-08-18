// Package observability — worker_metrics.go (RW-PROD-013, June 2026).
//
// 16 worker_* Prometheus metric vars that surface the remote worker
// runtime (heartbeat / task slots / resource use / failure modes).
// All vars are package-level promauto collectors so they auto-register
// with the global prometheus.DefaultRegisterer — same convention
// as the rest of metrics.go.
//
// Naming conventions
// -----------------
// Gauges do NOT carry the `_total` suffix; counters ALWAYS do. Time
// gauges use `_seconds` suffix; byte gauges use `_bytes`; ratios use
// `_ratio` suffix. The Prometheus Go client is opinionated about
// suffix families — drifting from them opens lint findings on
// downstream Prometheus rules in config/alerting_rules.yml.
//
// Label conventions
// -----------------
// Per-worker gauges carry `worker_id` as primary label. Stateful
// metrics (active tasks, disk per-mount, network per-device) also
// carry a semantic 2nd label so dashboards can filter on the
// auxiliary dimension. Counters carry the failure-mode-axis label
// directly (reason / kind) — counter increments are typically fire-
// and-forget so the worker_id label sits on the gauge side (where
// latest status is meaningful) rather than on every counter bump.
//
// The label key list exactly matches what worker_metrics_test.go
// passes to With* primitives — DO NOT change cardinality or
// ordering without updating the test in lock-step.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

// Session / liveness (gauges) ───────────────────────────────────────

// ── Log field-key constants (canonical names) ──────────────────────
//
// These constants pin the EXACT names that production log lines use
// for per-worker, per-job, per-task, per-attempt, and per-correlation
// attribution. The metric-side primary label (e.g. worker_id on
// every per-worker gauge/counter) MUST match these strings so a
// single PromQL / log query can join both surfaces by the same key.
//
// Renaming ANY of these is a breaking change for every downstream
// dashboard, alert rule, and log query that joins on the field. The
// TestLogKeyConstantsStable test pins the values so a rename cannot
// land silently.
const (
	LogKeyWorkerID      = "worker_id"
	LogKeySessionID     = "session_id"
	LogKeyJobID         = "job_id"
	LogKeyTaskID        = "task_id"
	LogKeyAttemptID     = "attempt_id"
	LogKeyCorrelationID = "correlation_id"
)

// Session / liveness (gauges) ─────────────────────────────────────────

// WorkerSessionActive is 1 when the worker session is currently
// active (heartbeat within TTL), else 0. environment label
// partitions production vs. staging vs. canary workers so dashboards
// can scope alerts to one fleet.
var WorkerSessionActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "worker_session_active",
	Help: "Worker session liveness (1 if heartbeat within TTL, 0 if stale). 0 is the source of truth for \"worker offline\" alerts; partition by environment.",
}, []string{"worker_id", "environment"})

// WorkerHeartbeatAgeSeconds is the age in seconds of the worker's
// last heartbeat. Used as the canonical "is this worker live right
// now" gauge. Pairs with WorkerSessionActive which is the binary
// latched view.
var WorkerHeartbeatAgeSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "worker_heartbeat_age_seconds",
	Help: "Age in seconds of the worker's most recent heartbeat. Pairs with worker_session_active for staleness alerts.",
}, []string{"worker_id"})

// Task scheduling (gauges) ───────────────────────────────────────────

// WorkerActiveTasks is the in-flight task count, partitioned by
// task_type ("_" is the untyped bucket — e.g. generic pipelined
// work without a specific subtype). Dashboards read this alongside
// WorkerTaskSlots to compute saturation.
var WorkerActiveTasks = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "worker_active_tasks",
	Help: "Number of tasks currently in-flight on this worker, by task_type. Saturate against worker_task_slots for backpressure.",
}, []string{"worker_id", "task_type"})

// WorkerTaskSlots is the configured task-slot capacity (static per
// worker). The ratio worker_active_tasks / worker_task_slots is the
// canonical saturation metric for the worker fleet.
var WorkerTaskSlots = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "worker_task_slots",
	Help: "Total task slots available on this worker. Saturation = worker_active_tasks / worker_task_slots.",
}, []string{"worker_id"})

// Resource use (gauges) ─────────────────────────────────────────────

// WorkerCPUUtilizationRatio is the worker's CPU utilization as a
// ratio in [0.0, 1.0]. Sampled by the heartbeat payload, not by
// /proc polling per scrape.
var WorkerCPUUtilizationRatio = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "worker_cpu_utilization_ratio",
	Help: "Worker CPU utilization as a ratio in [0.0, 1.0]. Sampled at heartbeat; not scraped from /proc.",
}, []string{"worker_id"})

// WorkerMemoryRSSBytes is the worker's resident memory in bytes.
// Reported by the heartbeat payload — independent of Go's
// runtime.MemStats (which is what /metrics auto-exposes).
var WorkerMemoryRSSBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "worker_memory_rss_bytes",
	Help: "Worker resident-set size in bytes. Reported by heartbeat, complements Go runtime memory gauges.",
}, []string{"worker_id"})

// WorkerMemoryUsedBytes is the worker's process-memory delta in
// bytes. Useful for detecting lax allocations that go unnoticed by
// RSS-only monitors.
var WorkerMemoryUsedBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "worker_memory_used_bytes",
	Help: "Worker process-used memory in bytes (allocations net of GC). Delta-friendly for trend dashboards.",
}, []string{"worker_id"})

// WorkerDiskFreeBytes is the worker's free disk space, partitioned
// by mount path so operators see which volume is filling (data vs.
// temp vs. uploads).
var WorkerDiskFreeBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "worker_disk_free_bytes",
	Help: "Worker free disk bytes by mount path. Pair with worker_temp_bytes to spot temp/log volume shrinkage before data volume fills.",
}, []string{"worker_id", "mount_path"})

// WorkerTempBytes is the worker's temp-directory footprint in bytes.
// Trending up monotonically between runs suggests an unlinked-temp
// leak (e.g. upload staging dir not cleaned on Fail path).
var WorkerTempBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "worker_temp_bytes",
	Help: "Worker temp-directory footprint in bytes. Alert on monotonic growth between worker restarts.",
}, []string{"worker_id"})

// WorkerNetworkRxBytes is cumulative network bytes received, by
// device (lo, eth0, ...). Read as a delta for throughput dashboards;
// the gauge itself is cumulative-since-start.
var WorkerNetworkRxBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "worker_network_rx_bytes",
	Help: "Cumulative network bytes received by this worker, by device. Use rate() for throughput.",
}, []string{"worker_id", "device"})

// WorkerNetworkTxBytes is cumulative network bytes transmitted, by
// device (lo, eth0, ...). Pair with WorkerNetworkRxBytes for
// upload-bandwidth trend dashboards (relevant for artifact-upload
// flow rates).
var WorkerNetworkTxBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "worker_network_tx_bytes",
	Help: "Cumulative network bytes transmitted by this worker, by device. Use rate() for throughput.",
}, []string{"worker_id", "device"})

// Failure modes (counters) ──────────────────────────────────────────

// WorkerReconnectTotal counts reconnect events by reason ("initial"
// for pre-first-heartbeat reconnect loops, "heartbeat_lost" for
// mid-session heartbeat loss, "tcp_reset" for socket-level reset).
// Why a counter and not a gauge: per-worker reconnect counts are
// cumulative-since-start and monotonic.
var WorkerReconnectTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "worker_reconnect_total",
	Help: "Total reconnect attempts by reason (initial, heartbeat_lost, tcp_reset, ...). Monotonic per worker run.",
}, []string{"reason"})

// WorkerLeaseRenewalFailuresTotal counts lease-renewal failures by
// reason ("timeout", "auth_invalid", "raced", "master_unreachable").
// A non-zero rate on this counter is the canonical "the lease loop
// is unhealthy" signal.
var WorkerLeaseRenewalFailuresTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "worker_lease_renewal_failures_total",
	Help: "Total lease-renewal failures by reason (timeout, auth_invalid, raced, master_unreachable). Non-zero rate means unhealthy lease loop.",
}, []string{"reason"})

// WorkerArtifactUploadFailuresTotal counts artifact-upload failures
// by reason ("checksum_mismatch", "s3_unreachable", "auth_invalid",
// "context_timeout"). Pairs with the canary artifact-integrity gate
// (RW-PROD-008) — a sustained non-zero rate means the canary will
// fail and worker promotion stalls.
var WorkerArtifactUploadFailuresTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "worker_artifact_upload_failures_total",
	Help: "Total artifact-upload failures by reason (checksum_mismatch, s3_unreachable, auth_invalid, context_timeout).",
}, []string{"reason"})

// WorkerFinalizationDBLockedTotal counts artifact-finalization attempts
// that hit SQLITE_BUSY / SQLITE_LOCKED ("database is locked") in the
// JobFinalizer's media_assets upsert, partitioned by job_type and
// outcome ("retried" = a bounded retry absorbed it, "terminal" = retries
// were exhausted). A non-zero "terminal" rate is the canonical signal
// that finalization is failing despite retries and jobs risk being
// orphaned in RUNNING until the lease scanner requeues them.
var WorkerFinalizationDBLockedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "worker_finalization_db_locked_total",
	Help: "Total artifact-finalization attempts hitting SQLITE_BUSY/SQLITE_LOCKED, by job_type and outcome (retried|terminal).",
}, []string{"job_type", "outcome"})

// WorkerFallbackTotal counts production fallback activations by
// kind ("downgraded_path", "stale_cache_return", "skip_optimization").
// The certification gate (worker-certification-checklist.md §3) bans
// production fallbacks; any non-zero rate in production worker
// durations fails the promotion.
var WorkerFallbackTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "worker_fallback_total",
	Help: "Total production-fallback activations by kind (downgraded_path, stale_cache_return, skip_optimization).",
}, []string{"kind"})

// WorkerEmergencyPathTotal counts emergency-path activations by
// kind ("mount_drive_via_python", "bypass_normalizer",
// "ignore_artifact_checksum"). Like WorkerFallbackTotal, ANY rate in
// production fails the certification gate (production has
// no-fallback invariant).
var WorkerEmergencyPathTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "worker_emergency_path_total",
	Help: "Total emergency-path activations by kind (mount_drive_via_python, bypass_normalizer, ignore_artifact_checksum).",
}, []string{"kind"})

// ── Telemetry silent-drop counters (PR-GODOBJ-14-WORKER-REGISTRY, FASE 0.2, July 4 2026) ─
//
// PR-GODOBJ-14-WORKER-REGISTRY closure (part of GODOBJ-2026-07-03
// god-object decomposition wave). Pre-FASE-0.2 the worker package
// carried several `_ = X(...)` + `_, _ := X(...)` silent-drops in
// the Progress / Event / IsCancelled emit paths — godlike/07
// no-fake-availability violation. Post-FASE-0.2 every emit failure
// bumps one of these 3 counters BEFORE propagating (or non-fatally
// failing-closed) the error so the operator dashboard can alert
// on the failure mode.
//
// Cardinality bound (godlike/07): job_type (~30 distinct types
// today, bounded enumerator not a free string) × outcome (~3
// values: success/error/dropped) × reason (~4 values:
// broker_emit_failed / add_event_failed / is_cancelled_err /
// lease_renew_lost) = ~360 series per metric. Well under Prometheus
// cardinality guidance. The worker_id dimension is intentionally
// NOT included (matches the convention documented at the top of
// this file: counters carry failure-mode-axis labels, per-worker
// liveness lives on the gauge side).
//
// godlike/06 SSOT (one-canonical-owner-per-fact): the 3 counters
// below own "did telemetry emit succeed or fail" for workers.
// WorkerReconnectTotal + WorkerLeaseRenewalFailuresTotal +
// WorkerArtifactUploadFailuresTotal + WorkerFallbackTotal +
// WorkerEmergencyPathTotal (above) own lease / artifact / fallback
// / emergency paths distinctly so dashboards can split the alert
// surface per failure-mode.
//
// FASE 0.2 SCOPE:
//   (a) worker_progress_emitted_total: every Progress emit attempt.
//   (b) worker_progress_errors_total: Progress emit failures.
//   (c) worker_event_drops_total: events that worker.Tools.dropped
//       (in the Event closure we surface as counters because the
//       underlying Event port is not yet wired through worker.Tools).
//
// Tracking: architecture/current.yaml#GODOBJ-2026-07-03.linked_issues
// [PR-GODOBJ-14-WORKER-REGISTRY].

// WorkerProgressEmittedTotal counts worker Progress emit attempts
// by job_type and outcome (success | error). Pair with
// WorkerProgressErrorsTotal for the failure-mode attribution; a
// non-zero rate on the "error" axis means telemetry emit is
// silently breaking on this job type (godlike/07 no-fake-availability
// surface).
var WorkerProgressEmittedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "worker_progress_emitted_total",
	Help: "Total worker Progress emit attempts by job_type and outcome (success|error). Pairs with worker_progress_errors_total for failure attribution.",
}, []string{"job_type", "outcome"})

// WorkerProgressErrorsTotal counts worker Progress emit FAILURES
// by job_type and reason (broker_emit_failed | lease_lost |
// context_canceled | unknown). A sustained non-zero rate is the
// canonical "telemetry is breaking on this job_type" signal — the
// operator dashboard reads this metric to alert on silent-dropped
// progress emissions (the pre-FASE-0.2 failure mode).
var WorkerProgressErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "worker_progress_errors_total",
	Help: "Total worker Progress emit FAILURES by job_type and reason (broker_emit_failed|lease_lost|context_canceled|unknown). Sustained non-zero rate = telemetry silently broken on this job_type.",
}, []string{"job_type", "reason"})

// WorkerEventDropsTotal counts worker Event emit attempts that
// dropped silently because the underlying AddEvent (or vendor
// equivalent) returned an error. Pre-FASE-0.2 the wrapper at
// repository_lifecycle.go::SetProgress used `_ = r.AddEvent(...)`
// — zero telemetry on the drop. Post-FASE-0.2 the wrapper now
// emits WorkerEventDropsTotal with a job_type label when available.
//
// Cardinality note: job_type label is "" today because the
// SetProgress wrapper is infra-layer and does not see the job_type
// (the job_id is passed but the addition of job_type would require
// a join with jobs.type). Forward-pointer PR-Telemetry-AddEvent-
// Infra-Type threads job_type through; deadline TBD. Until then,
// the "" label partitions "infra-level drops" from per-job_type
// drops counted by the worker-package sites.
var WorkerEventDropsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "worker_event_drops_total",
	Help: "Total worker events silently dropped due to DB error. Pre-FASE-0.2 the drop had zero telemetry (godlike/07 no-fake-availability).",
}, []string{"job_type"})

// ── Logger decorator ───────────────────────────────────────────────
// WithWorker attaches worker_id + session_id fields to the
// supplied zap.Logger. Calling convention: production handlers
// (session bootstrap, heartbeat handler, etc.) wrap their logger
// at the entry point and pass the returned logger down the call
// chain so every log line emitted within a worker run carries the
// canonical (worker_id, session_id) attribution.
//
// Short-circuit rule: when both workerID and sessionID are empty,
// the input logger is returned unchanged. This avoids paying a
// do-nothing With() cost in code paths that pre-date the
// worker/session bootstrap (e.g. early health checks). Caller is
// expected to switch to the decorated logger once the session is
// established.
//
// Tests: TestWithWorker_SkipOnEmpty,
//
//	TestWithWorker_NonEmptyReturnsDifferent.
func WithWorker(logger *zap.Logger, workerID, sessionID string) *zap.Logger {
	if logger == nil {
		return nil
	}
	if workerID == "" && sessionID == "" {
		return logger
	}
	return logger.With(
		zap.String(LogKeyWorkerID, workerID),
		zap.String(LogKeySessionID, sessionID),
	)
}
