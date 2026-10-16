// Package metrics owns the Prometheus collectors for the central job queue.
// It exposes the operational, realtime aggregates that complement the
// per-job timing already persisted in PostgreSQL (render_attempts and
// processing_metrics): queue depth, render duration, queue wait and lease
// expiry.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

// Metrics holds the registered Prometheus collectors for the queue.
type Metrics struct {
	registry *prometheus.Registry

	JobsPending    prometheus.Gauge
	RenderDuration prometheus.Histogram
	QueueWait      prometheus.Histogram
	LeaseExpired   prometheus.Counter
	WorkersReady   prometheus.Gauge
	// OldestPending exposes the head-of-line age signal Depth cannot show:
	// a flat pending count with a rising value means the queue tail is
	// stagnant (claim capacity gone or a head job blocking FIFO).
	// WorkersDegraded/WorkersStale expose the heartbeat-age warning bands
	// between "ready" and "offline". They exist so a fleet that is slowly
	// losing its workers is visible BEFORE it has none: a worker whose
	// heartbeat has aged past a third of the staleness window is already
	// reported as nothing but a stale row on /workers (see the queue's
	// worker-liveness contract), while a gauge that only counts dead workers
	// shows nothing until the fleet is gone.
	WorkersDegraded prometheus.Gauge
	WorkersStale    prometheus.Gauge
	WorkersOffline  prometheus.Gauge
	OldestPending   prometheus.Gauge
}

// New constructs a Metrics with its own registry and registers all collectors.
func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		JobsPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "renderinggen_jobs_pending",
			Help: "Number of jobs currently waiting to be claimed.",
		}),
		RenderDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "renderinggen_render_duration_seconds",
			Help:    "Time from claim to completion for a finished render attempt.",
			Buckets: prometheus.DefBuckets,
		}),
		QueueWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "renderinggen_queue_wait_seconds",
			Help:    "Time a job spent waiting in the queue before being claimed.",
			Buckets: prometheus.DefBuckets,
		}),
		LeaseExpired: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "renderinggen_lease_expired_total",
			Help: "Number of jobs whose lease expired and were requeued or failed.",
		}),
		WorkersReady: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "renderinggen_workers_ready",
			Help: "Number of rendering workers whose heartbeat is fresh enough that they are capacity.",
		}),
		WorkersDegraded: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "renderinggen_workers_degraded",
			Help: "Number of rendering workers whose heartbeat has aged past a third of the staleness window (one missed heartbeat).",
		}),
		WorkersStale: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "renderinggen_workers_stale",
			Help: "Number of rendering workers whose heartbeat has aged past two thirds of the staleness window.",
		}),
		WorkersOffline: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "renderinggen_workers_offline",
			Help: "Number of rendering workers whose heartbeat is outside the staleness window (or absent).",
		}),
		OldestPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "renderinggen_oldest_pending_seconds",
			Help: "Age of the oldest still-pending job, measured from queued_at. Zero when the queue is empty. Rising with a flat jobs_pending means the head of the queue is stuck.",
		}),
	}
	m.registry.MustRegister(m.JobsPending, m.RenderDuration, m.QueueWait, m.LeaseExpired,
		m.WorkersReady, m.WorkersDegraded, m.WorkersStale, m.WorkersOffline, m.OldestPending)
	return m
}

// Handler returns the HTTP handler exposing the metrics in the Prometheus
// text exposition format for scraping.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Gather returns the current metrics snapshot for tests and diagnostics.
func (m *Metrics) Gather() ([]*dto.MetricFamily, error) {
	if m == nil {
		return nil, nil
	}
	return m.registry.Gather()
}
