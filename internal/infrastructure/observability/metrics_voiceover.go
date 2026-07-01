// Package observability — voiceover metrics (P0 #4 commit B/2, July 2026).
//
// Voiceover-related Prometheus counters/gauges/histograms live here
// per the pkg observability "split-by-feature" pattern documented in
// metrics_registry.go: each domain (jobs, qdrant, scripts, media)
// owns its own metrics_*.go file. Voiceover's metrics_voiceover.go
// declares the orphan-sweeper counters plus the future humanizer
// (alias for legibility on the application side).
//
// Backing metrics for the orphan sweeper (consumed in
// internal/application/voiceover/orphan_sweeper.go):
//   - orphan_sweeper_runs_total (counter, no labels)
//   - orphan_sweeper_reconciled_total (counter, label: outcome)
//
// Production wiring: the orphan sweeper accepts a *Metrics struct
// (see voiceover.Metrics) constructed from these package-level
// vars. Tests construct local prometheus.NewCounter (NOT promauto)
// to avoid polluting the default registry.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// OrphanSweeperRunsTotal is incremented ONCE per OrphanSweeper.Run
	// invocation (per process boot, NOT per sweep tick). "ad ogni
	// start" of the goroutine per user-spec for the orphan sweeper.
	OrphanSweeperRunsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "orphan_sweeper_runs_total",
		Help: "Total number of OrphanSweeper.Run() goroutine starts (once per process boot, NOT per sweep tick).",
	})

	// OrphanSweeperReconciledTotal is incremented per stale-row
	// compensation. Labels:
	//   outcome="uploaded_cleanup" — Drive.Trash + MarkFailed on
	//                                a stale 'uploaded' row (Drive-side
	//                                orphan recovered).
	//   outcome="pending_timeout"  — MarkFailed (NO Drive action)
	//                                on a stale 'pending' row (the
	//                                Drive file was never created).
	OrphanSweeperReconciledTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orphan_sweeper_reconciled_total",
		Help: "Total number of stale upload_intents rows reconciled by the orphan sweeper, partitioned by outcome.",
	}, []string{"outcome"})
)
