// Package observability — outbox events pool metrics (Fase 6(c) Push 6.2, July 2026).
//
// SRE surface for the outbox_events worker pool
// (internal/platform/sqlite/outboxevents/Pool).
// Five Prometheus collectors, all registered via promauto against
// the default registry at package init so /metrics (api/routes.go)
// exposes them on the standard scrape path.
//
// Cardinality (bounded):
//   - event_type: currently 8 canonical strings
//     (asset.index.requested, asset.index.delete_requested,
//     asset.drive.delete_requested, asset.index.restore_requested,
//     delivery.requested, asset.metadata_export.requested,
//     provider.sync.requested, voiceover.cleanup.requested,
//     workflow.step.*, asset.published) — every domain
//     grows with new event-type additions, but the catalog is
//     finite.
//   - reason label on Retries: 2 values (transient|terminal).
//   - outcome label on Duration: 3 values (ok|err|dlq).
//     Total series: ~8 × (2 + 3) = 40 per labelled metric, plus 2
//     unlabelled gauges (lag, reclaim). Bounded; no fan-out risk.
//
// Failure mode (godlike/07 NO-FAKE-AVAILABILITY):
//   - Collectors are declared at package init via promauto.New*.
//     A name collision (e.g. duplicate registration by human error)
//     panic-fails at process start. Prometheus ALREADY registers the
//     default Go runtime collectors, but the custom names below are
//     namespaced to `outbox_*` so collisions with delivery_*/media_*
//     metrics are structurally impossible.
//   - The metrics are pointers (New*Vec returns *Vec) — a nil pointer
//     dereference on .Inc / .Observe would panic at runtime, but the
//     promauto guarantee is that registered metrics are non-nil at
//     init. Tests passing a fresh registry MUST re-export each
//     metric explicitly (see metrics_outbox_test.go for the pattern).
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// defaultOutboxDurationBuckets is the canonical Prometheus latency
// bucket set for one-shot outbox dispatch (no stream/processing).
// Sub-millisecond (typical Qdrant upsert under nominal load) up to
// 10s (Webhook receiver that hit a downstream queueing stall).
var defaultOutboxDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

var (
	// OutboxLagSeconds is the canonical lag gauge: time between an
	// event's created_at (the canonical SQL column from migration 092)
	// and the moment the worker pool first observed it (claim time).
	// Labels: event_type.
	//
	// Operator reads:
	//   - rising lag on a single event_type → upstream producer stalled
	//     OR worker pool saturated (backlog builds before dispatch).
	//   - global rising lag → general throughput collapse (worker
	//     count too low, DB contention, or upstream write rate > drain).
	OutboxLagSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "outbox_lag_seconds",
		Help: "Lag between outbox event created_at and first worker claim, partitioned by event_type. Rising values indicate upstream producer stall or worker pool saturation.",
	}, []string{"event_type"})

	// OutboxDispatchDurationSeconds is the canonical per-dispatch
	// histogram: time between claim and finalise (MarkCompleted /
	// MarkDeadLetter / MarkSuperseded). Labels: event_type, outcome
	// (ok | err | dlq).
	//
	// Buckets cover sub-millisecond (Qdrant upsert under nominal
	// load) up to 10s (Webhook receiver downstream-queueing stall).
	// Buckets are wider on the high end than drive_roots_validator's
	// because dispatch can include a full Qdrant.Upsert round-trip
	// + SQLite transaction + IndexState flip.
	OutboxDispatchDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "outbox_dispatch_duration_seconds",
		Help:    "Per-event dispatch duration, partitioned by event_type and outcome (ok|err|dlq).",
		Buckets: defaultOutboxDurationBuckets,
	}, []string{"event_type", "outcome"})

	// OutboxReclaimTotal counts RequeueExpiredLeases invocations and
	// the total rows reclaimed per call. Labels: none (the bucket of
	// rows reclaimed per call varies — gauge alternative would
	// double-track; we accept the per-call counter).
	//
	// Operator reads:
	//   - rising rate → workers are crashing or being killed before
	//     honouring the lease-TTL; investigate worker-side
	//     cancellation.
	//   - flat at long-running deployments → healthy storm recovery.
	OutboxReclaimTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "outbox_reclaim_total",
		Help: "Cumulative count of RequeueExpiredLeases calls that returned at least one reclaimed row from a previous worker's expired lease.",
	})

	// OutboxDLQTotal counts events that reached dead_letter status
	// after exhausting max_attempts OR on terminal-error classification
	// at first attempt. Labels: event_type.
	//
	// Operator reads:
	//   - rising DLQ on asset.index.* → Qdrant or media_assets SQL
	//     failing systematically; investigate.
	//   - spikes correlate with upstream schema-version mismatches
	//     (each TerminalError short-circuits to DLQ).
	OutboxDLQTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "outbox_dlq_total",
		Help: "Cumulative count of events promoted to dead_letter status, partitioned by event_type. Spikes correlate with persistent systematic failures (Qdrant outage, schema drift) or per-event TerminalError classifications.",
	}, []string{"event_type"})

	// OutboxRetriesTotal counts non-terminal handler errors that
	// triggered MarkFailed (i.e. the event is being scheduled for
	// re-attempt, not DLQ'd). Labels: event_type only.
	//
	// Why no `reason` label: terminal errors (NewTerminalError)
	// short-circuit to DLQ BEFORE reaching this counter call site.
	// A `reason="terminal"` label was prototyped (pre-finalize) and
	// rejected because that value would never increment — the
	// godlike/07 NO-FAKE-AVAILABILITY contract forbids dashboards
	// that query a label value with stable zero. Operator reading:
	// this metric counts ONLY transient-error retries; terminal
	// events are visible on OutboxDLQTotal instead.
	//
	// Operator reads:
	//   - rate correlated with Upstream-rate → rate-limited upstream
	//     (Qdrant quota, Drive quota, etc.).
	//   - rate correlated with DLQ rate → handler logic emitting
	//     terminal-vs-transient inconsistently.
	//   - flat at long-running deployments → healthy retry budget.
	OutboxRetriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "outbox_retries_total",
		Help: "Cumulative count of events scheduled for retry via MarkFailed (after non-terminal handler error); transient retries only — terminal errors count on OutboxDLQTotal instead.",
	}, []string{"event_type"})

	// MediaOutboxStatusCount is the current PostgreSQL media-outbox row
	// count, partitioned by event type and lifecycle status. The worker reads
	// this from the PostgreSQL SSOT and explicitly sets both observed statuses,
	// including zero, so a drained queue is visible rather than stale.
	//
	// The composition root currently registers the clip.render Drive delivery
	// event. Keeping event_type as a bounded label allows image delivery and
	// future media events to reuse the same canonical projection without a
	// second metric family.
	MediaOutboxStatusCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "media_outbox_status_count",
		Help: "Current PostgreSQL media outbox row count by event type and lifecycle status.",
	}, []string{"event_type", "status"})
)

// ObserveOutboxStatus is the composition adapter consumed by the media
// worker. It deliberately exposes only the projection operation; SQL remains
// owned by internal/platform/postgres/media.
func (m *MediaOutboxStatusCountAdapter) ObserveOutboxStatus(eventType, status string, count int64) {
	if m == nil || eventType == "" || status == "" {
		return
	}
	MediaOutboxStatusCount.WithLabelValues(eventType, status).Set(float64(count))
}

// MediaOutboxStatusCountAdapter adapts the canonical media worker status
// observer to Prometheus collectors.
type MediaOutboxStatusCountAdapter struct{}

// NewMediaOutboxStatusCountAdapter returns the default Prometheus adapter.
func NewMediaOutboxStatusCountAdapter() *MediaOutboxStatusCountAdapter {
	return &MediaOutboxStatusCountAdapter{}
}
