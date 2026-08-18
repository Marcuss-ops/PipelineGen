package observability

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// FASE 2 (August 2026): job-run level metrics fed by the kernel
// observability RunObserver. The runtimes (orchestrator runJob +
// remote worker runLease) finish one Run per attempt; the
// RunReportsCollector adapter translates each finished report into
// low-cardinality Prometheus observations.
//
// Label policy (per the global contract): ONLY job_type and status are
// labels (low cardinality). job_id, run_id, URLs, query text, clip_id,
// asset_id and segment_id NEVER become labels — they stay in the
// database and structured logs.
//
// Attempt accounting: a run report carries AttemptID (the canonical
// per-claim lease token) and Counters.Retries; the 1-based attempt
// number is RetryCount+1 (documented in the kernel ClaimRunInfo).
var (
	jobRunDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "job_run_duration_seconds",
		Help:    "End-to-end wall time of one job run (attempt), by job type and terminal status. Attempt number = retry_count + 1.",
		Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600, 900, 1800, 3600},
	}, []string{"job_type", "status"})

	jobRunQueueWaitSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "job_run_queue_wait_seconds",
		Help:    "Time a job spent waiting in queue before the worker claimed it, by job type.",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120},
	}, []string{"job_type"})

	jobRunRetriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "job_run_retries_total",
		Help: "Total retries consumed by job runs, by job type (job.RetryCount snapshot at claim time).",
	}, []string{"job_type"})

	// Canonical operation / stage observations projected into Prometheus.
	// These are the LIVE projection of the observability SSOT: a boundary's
	// own direct Observe() timer is redundant once its canonical operation is
	// recorded, because the same duration flows here with stable,
	// low-cardinality labels (component/operation/stage/status only).
	jobOperationDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "job_operation_duration_seconds",
		Help:    "Duration of one canonical operation observation (external-boundary call), by component, operation and status.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	}, []string{"component", "operation", "status"})

	jobStageDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "job_stage_duration_seconds",
		Help:    "Duration of one canonical stage observation, by stage name and status.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600},
	}, []string{"stage", "status"})
)

func init() {
	prometheus.MustRegister(
		jobRunDurationSeconds, jobRunQueueWaitSeconds, jobRunRetriesTotal,
		jobOperationDurationSeconds, jobStageDurationSeconds,
	)
}

// RunReportsCollector implements kernobs.Collector. It holds the
// histogram/counter vectors as fields so tests can inject hermetic
// isolated vectors (mirrors the ObservabilityMetricsRecorder pattern
// in metrics_adapter_test.go).
type RunReportsCollector struct {
	duration  *prometheus.HistogramVec
	queueWait *prometheus.HistogramVec
	retries   *prometheus.CounterVec
	operation *prometheus.HistogramVec
	stage     *prometheus.HistogramVec
}

// NewRunReportsCollector wires the production (default-registry)
// metric vectors.
func NewRunReportsCollector() *RunReportsCollector {
	return &RunReportsCollector{
		duration:  jobRunDurationSeconds,
		queueWait: jobRunQueueWaitSeconds,
		retries:   jobRunRetriesTotal,
		operation: jobOperationDurationSeconds,
		stage:     jobStageDurationSeconds,
	}
}

// newRunReportsCollectorWithVectors is the hermetic-test constructor.
func newRunReportsCollectorWithVectors(
	duration *prometheus.HistogramVec,
	queueWait *prometheus.HistogramVec,
	retries *prometheus.CounterVec,
	operation *prometheus.HistogramVec,
	stage *prometheus.HistogramVec,
) *RunReportsCollector {
	return &RunReportsCollector{duration: duration, queueWait: queueWait, retries: retries, operation: operation, stage: stage}
}

// Collect implements kernobs.Collector. Best-effort and nil-tolerant:
// a metrics backend failure must never fail the job that produced the
// report, and a nil receiver / nil vector short-circuits to a no-op
// (partial-deploy + test-fixture path).
func (c *RunReportsCollector) Collect(_ context.Context, report *kernobs.RunReport) error {
	if c == nil || report == nil {
		return nil
	}
	jobType := report.JobType
	if jobType == "" {
		jobType = "unknown"
	}
	// Per-vector guards (mirrors the ObservabilityMetricsRecorder
	// pattern): a partially-wired collector still emits the vectors it
	// has instead of dropping all telemetry.
	if report.WallTimeMs > 0 && c.duration != nil {
		c.duration.WithLabelValues(jobType, report.Status).Observe(float64(report.WallTimeMs) / 1000.0)
	}
	if report.QueueWaitMs > 0 && c.queueWait != nil {
		c.queueWait.WithLabelValues(jobType).Observe(float64(report.QueueWaitMs) / 1000.0)
	}
	if report.Counters.Retries > 0 && c.retries != nil {
		c.retries.WithLabelValues(jobType).Add(float64(report.Counters.Retries))
	}
	// Canonical operation + stage observations are the SSOT: project them
	// into low-cardinality histograms so a boundary's own direct Observe()
	// timer becomes redundant once its canonical operation is wired.
	for _, op := range report.Operations {
		if op.DurationMs > 0 && c.operation != nil {
			c.operation.WithLabelValues(labelOrUnknown(op.Component), labelOrUnknown(op.Operation), labelOrUnknown(op.Status)).Observe(float64(op.DurationMs) / 1000.0)
		}
	}
	for _, st := range report.Stages {
		if st.DurationMs > 0 && c.stage != nil {
			c.stage.WithLabelValues(labelOrUnknown(st.Name), labelOrUnknown(st.Status)).Observe(float64(st.DurationMs) / 1000.0)
		}
	}
	return nil
}

// labelOrUnknown keeps an empty label value out of Prometheus by falling
// back to the stable "unknown" token (mirrors the job-type fallback).
func labelOrUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}
