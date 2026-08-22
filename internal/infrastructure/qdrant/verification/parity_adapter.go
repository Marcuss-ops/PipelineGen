// Package verification — parity_adapter.go (August 2026): the
// infrastructure adapters satisfying the application-layer
// projectionreconciler ports (Pattern 0 — AGENTS.md).
//
// The periodic projection reconciler (internal/application/qdrant/
// projectionreconciler) measures eligible-vs-active parity every tick.
// Its two ports are wired to these adapters:
//
//   - ProjectionParityCheckerAdapter — wraps the ProjectionVerifier
//     (the SAME verifier behind the verify-projection admin command)
//     so the periodic signal and the operator-facing gate share one
//     boundary: eligible = SQLiteAssetStore.ListAllAssetIDs
//     (SearchIndexEligibilitySQL SSOT), active = scroll of the runtime
//     alias target.
//   - ParityMetricsAdapter — emits the Prometheus gauges declared in
//     internal/infrastructure/observability/metrics_media.go
//     (projection_coverage_ratio, projection_orphan_count, ...).
package verification

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/projectionreconciler"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// Compile-time assertions: the adapters satisfy the application ports.
var (
	_ projectionreconciler.ParityChecker = ProjectionParityCheckerAdapter{}
	_ projectionreconciler.Metrics       = ParityMetricsAdapter{}
)

// ProjectionParityCheckerAdapter adapts the ProjectionVerifier to the
// projectionreconciler.ParityChecker port.
type ProjectionParityCheckerAdapter struct {
	// Verifier is the underlying active-projection verifier. nil is a
	// wiring bug — the checker fails closed with a typed error.
	Verifier *ProjectionVerifier
}

// CheckProjectionParity implements projectionreconciler.ParityChecker.
func (a ProjectionParityCheckerAdapter) CheckProjectionParity(ctx context.Context) (projectionreconciler.ProjectionParity, error) {
	if a.Verifier == nil {
		return projectionreconciler.ProjectionParity{}, errProjectionVerifierNil
	}
	report, err := a.Verifier.VerifyActiveProjection(ctx)
	if err != nil {
		return projectionreconciler.ProjectionParity{}, err
	}
	return projectionreconciler.ProjectionParity{
		Collection:           report.Collection,
		EligibleSQLite:       report.EligibleSQLite,
		QdrantPoints:         report.QdrantPoints,
		MissingCount:         report.MissingCount,
		OrphanCount:          report.OrphanCount,
		PointsMissingAssetID: report.PointsMissingAssetID,
		CompleteScan:         report.CompleteScan,
	}, nil
}

// ParityMetricsAdapter emits the projection-reconciler Prometheus
// gauges. Zero-value struct; callers construct via
// `verification.ParityMetricsAdapter{}`.
type ParityMetricsAdapter struct{}

// ObserveParity implements projectionreconciler.Metrics.
func (ParityMetricsAdapter) ObserveParity(p projectionreconciler.ProjectionParity) {
	observability.ProjectionCoverageRatio.Set(p.CoverageRatio())
	observability.ProjectionOrphanCount.Set(float64(p.OrphanCount))
	observability.ProjectionMissingCount.Set(float64(p.MissingCount))
	observability.ProjectionEligibleSQLite.Set(float64(p.EligibleSQLite))
	observability.ProjectionQdrantPoints.Set(float64(p.QdrantPoints))
	scanComplete := 0.0
	if p.CompleteScan {
		scanComplete = 1.0
	}
	observability.ProjectionScanComplete.Set(scanComplete)
	observability.ProjectionReconcileRunsTotal.Inc()
	observability.ProjectionReconcileLastSuccess.SetToCurrentTime()
}

// ObserveError implements projectionreconciler.Metrics.
func (ParityMetricsAdapter) ObserveError() {
	observability.ProjectionReconcileErrorsTotal.Inc()
}

// errProjectionVerifierNil is the fail-closed sentinel for a checker
// adapter wired without its verifier.
var errProjectionVerifierNil = errors.New("projection parity checker: Verifier is nil (wiring bug — the periodic reconciler cannot measure parity without a verifier)")
