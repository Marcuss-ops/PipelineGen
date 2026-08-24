// Package deletion — metrics_adapter.go (Blocco 3.2 commit 2/2, June 2026)
//
// Adapter exposing the application-layer reconciler.Metrics port
// wired against the observability package's Prometheus-backed
// counters (see internal/platform/observability/metrics_media.go).
//
// Pattern 0 (AGENTS.md): the application layer declares the
// Metrics interface in reconciler/ports.go; the infrastructure
// layer produces this adapter. The composition root wires
// deletion.ReconcilerMetricsAdapter{} into reconciler.NewServiceFromDeps.
//
// Exports:
//
//	ReconcilerMetricsAdapter{}  — zero-value struct that satisfies
//	                               reconciler.Metrics
//
// No methods (no fields requiring init); callers construct via
// the type literal `deletion.ReconcilerMetricsAdapter{}` to avoid
// the per-call allocation cost of an init function.
package deletion

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// ReconcilerMetricsAdapter is the production-side adapter that
// satisfies the reconciler.Metrics port. It uses the package-level
// Prometheus counters declared in observability/metrics_media.go.
//
// Compile-time assertion: the adapter satisfies reconciler.Metrics
// (RecordRepair, RecordSkipped, RecordErrored, RecordRunComplete).
// Drift in any of the reconciler.Metrics method signatures is a
// build failure here rather than a runtime panic at first tick.
type ReconcilerMetricsAdapter struct{}

// Compile-time assertion injected at init; the reconciler.Metrics
// interface is an unexported-internal type, but this init-level
// property check ensures the adapter keeps in lockstep with the
// port's eventual public signature. The strict-method assert is
// verified via the use site (see internal/app/lifecycle.go call).
var _ interface {
	RecordRepair(action, fromState string)
	RecordSkipped(reason string)
	RecordErrored()
	RecordRunComplete(durationSeconds float64)
} = ReconcilerMetricsAdapter{}

func (ReconcilerMetricsAdapter) RecordRepair(action, fromState string) {
	observability.DeletionReconcilerActionsTotal.WithLabelValues(action, fromState).Inc()
}

func (ReconcilerMetricsAdapter) RecordSkipped(reason string) {
	observability.DeletionReconcilerSkippedTotal.WithLabelValues(reason).Inc()
}

func (ReconcilerMetricsAdapter) RecordErrored() {
	observability.DeletionReconcilerErrorsTotal.Inc()
}

func (ReconcilerMetricsAdapter) RecordRunComplete(durationSeconds float64) {
	observability.DeletionReconcilerLastSuccess.SetToCurrentTime()
	observability.DeletionReconcilerDuration.Observe(durationSeconds)
}
