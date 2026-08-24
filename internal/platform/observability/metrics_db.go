// Package observability — metrics_db.go (FASE 6 Cut 6.2, July 2026).
//
// Three Prometheus collectors for the SQLite reader/writer pool:
//
//   - connection_wait_seconds (HistogramVec, op=writer|reader):
//     time spent waiting to acquire a connection from database/sql's
//     pool queue. Single connection-pool slot contention under write
//     burst is the canonical "URI-induced busy-storm" symptom the
//     Cut 6.2 reader/writer split mitigates; this metric surfaces
//     the contention surface on dashboards.
//   - tx_duration_seconds (HistogramVec, op=writer|reader, outcome=ok|err):
//     end-to-end BeginTx..Commit wall-clock. "Ultra-short write tx"
//     contract: dashboard p99 should stay <5ms for the canonical
//     lease-fence UPDATE path; rising p99 indicates a stale
//     SELECT inside an open tx (Cut 6.2 anti-pattern).
//   - sqlite_busy_total (CounterVec, op=writer|reader):
//     increments on every classified SQLite SQLITE_BUSY / SQLITE_LOCKED
//     returned from the driver. The classifySQLiteError classifier
//     (registered at init() in sqlite/registry_retry_classifier.go) is
//     the canonical observer; the metric is the burn-rate evidence
//     trail.
//
// Cardinality (bounded):
//   - op: 2 values (writer, reader). Total series per metric: 2 × 3 = 6
//     for tx_duration (op × outcome), 2 for connection_wait, 2 for
//     busy_total. Bounded; no Prometheus fan-out risk.
//
// godlike/07 fail-closed: promauto-registered metrics are non-nil at
// process start. Tests that need hermetic counters construct a fresh
// promauto.With(reg)-scoped instance (see metrics_outbox_test.go for
// the pattern).
//
// Naming (godlike/06 SSOT): metric names match the user-spec literal
// "connection_wait_seconds" / "tx_duration_seconds" / "sqlite_busy_total"
// verbatim. Operators scrape /metrics (api/routes.go) and grep these
// exact names to identify Cut 6.2 instrumentation; renaming would
// silently break dashboards.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// defaultOpBuckets is the canonical bucket set for SQLite pool-side
// latency observation. Sub-millisecond (canonical ultra-short write
// tx; the lease-fence UPDATE pattern) up to 5s (writer contention
// burst under a runaway worker-loop blocking the writer conn slot).
var defaultOpBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

var (
	// ConnectionWaitSeconds is the canonical SQLite connection-pool
	// wait-latency histogram. Observed once per successful
	// `database/sql` acquire on both the writer pool (op=writer, the
	// single-slot writer at MaxOpenConns=1) and the reader pool
	// (op=reader, the N>1 reader pool).
	//
	// Operator reads:
	//   - rising p99 on op=writer → writer conn slot held too long
	//     by a runaway tx; investigate the worker holding the lease.
	//   - rising p99 on op=reader → reader pool exhausted (N too
	//     low for current workload); tune numReaders.
	//
	// Buckets are tuned for the canonical ultra-short write-tx
	// envelope (sub-millisecond target) with a long tail up to 5s
	// covering pathological contention bursts.
	ConnectionWaitSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "connection_wait_seconds",
		Help:    "Time spent waiting to acquire a connection from the SQLite pool (sec). Label op=writer|reader distinguishes the single-slot writer pool from the N>1 reader pool. Rising op=writer p99 indicates writer contention; rising op=reader p99 indicates reader pool starvation.",
		Buckets: defaultOpBuckets,
	}, []string{"op"})

	// TxDurationSeconds is the canonical per-transaction wall-clock
	// histogram. Observed once per tx exit on both writer and reader
	// pools; outcome distinguishes commit success (ok) from
	// rollback (err) — operators can read this as
	// "writer tx p99<5ms → ultra-short contract honoured" vs.
	// "writer tx p99>50ms → SELECT-inside-tx anti-pattern".
	//
	// Operator reads:
	//   - rising op=writer p99 → ultra-short write tx contract
	//     violated; investigate lease-fence BeginTx blocks (the
	//     canonical offender is a SELECT-rows-handle held through
	//     BeginTx — see requeueSingle fix in jobs/lifecycle_p1b_test.go).
	//   - rising op=writer err rate → commit failures; correlate
	//     with sqlite_busy_total{op=writer} for retry budget burn.
	//   - rising op=reader p99 → reader queries are doing too much
	//     work per tx (canonical offender: an aggregate query
	//     that should be split into N shorter reads).
	TxDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tx_duration_seconds",
		Help:    "Per-transaction wall-clock duration on the SQLite pool (sec). Labels: op=writer|reader, outcome=ok|err. op=writer p99 should remain <5ms (ultra-short write tx contract); rising p99 indicates a SELECT-handle held through BeginTx or a runaway aggregator.",
		Buckets: defaultOpBuckets,
	}, []string{"op", "outcome"})

	// SqliteBusyTotal counts SQLITE_BUSY / SQLITE_LOCKED errors
	// returned from the driver surface, classified by the
	// canonical classifySQLiteError at
	// internal/platform/sqlite/registry_retry_classifier.go.
	//
	// Operator reads:
	//   - rising op=writer busy_total → writer contention: a
	//     second writer tried to BEGIN while the single-slot
	//     writer was held. Investigate wall-clock of the held
	//     tx (correlate with tx_duration_seconds{op=writer}).
	//   - rising op=reader busy_total → reader contention under
	//     WAL: typically a backup/ANALYZE query holding the
	//     read-lock too long. Investigate reader-pool size.
	SqliteBusyTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sqlite_busy_total",
		Help: "Cumulative count of classified SQLITE_BUSY / SQLITE_LOCKED errors encountered, labelled by op=writer|reader. Dual reader/writer pool surfaces contention separately so a writer contention burst does not mask a reader contention symptom (and vice-versa).",
	}, []string{"op"})
)
