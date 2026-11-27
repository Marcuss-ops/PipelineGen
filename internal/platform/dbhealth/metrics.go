// Package dbhealth metrics (DB-08, 2026-11-27): the two health signals the
// nightly gate publishes as Prometheus series.
//
// They live here rather than in internal/platform/observability because both
// doctor implementations (SQLite in internal/platform/sqlite, PostgreSQL in
// internal/platform/postgres/media) already depend on this package, and neither
// may depend on the other. Keeping the collectors in the shared vocabulary
// package avoids an import cycle while still giving /metrics one definition per
// series.
package dbhealth

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// DBFKViolations is the number of row-level foreign-key violations found by
	// the last `admin db doctor` run, per plane. Non-zero is a blocking error in
	// the gate.
	DBFKViolations = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "db_fk_violations",
		Help: "Row-level foreign key violations found by the last `admin db doctor` run, by plane. Any non-zero value is a blocking finding.",
	}, []string{"plane"})

	// DBDeadTuples is the dead-tuple count per PostgreSQL media table at the
	// last doctor run. DB-06's autovacuum thresholds keep these near zero; a
	// sustained rise means the thresholds regressed or autovacuum is starved.
	DBDeadTuples = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "db_dead_tuples",
		Help: "Dead tuples per PostgreSQL media table at the last `admin db doctor` run. Sustained growth above the DB-06 threshold means autovacuum is not keeping up.",
	}, []string{"plane", "table"})
)
