// Package projectionreconciler — periodic active-projection parity
// reconciler (plan item #15, August 2026: "Reconcile continuo, non
// grandi emergenze").
//
// A lightweight periodic ticker that compares the canonical eligible
// SQLite asset set (SearchIndexEligibilitySQL SSOT) against the asset
// IDs present in the ACTIVE Qdrant projection and emits the operator
// metrics:
//
//	projection_coverage_ratio   eligible-present / eligible (1.0 = full)
//	projection_orphan_count     points in Qdrant without an eligible row
//	projection_missing_count    eligible rows absent from Qdrant
//	projection_eligible_sqlite  eligible SQLite count
//	projection_qdrant_points    active Qdrant point count
//	projection_scan_complete    1 when the scan completed cleanly
//
// The point of the periodic reconciler is drift detection: instead of
// discovering months later that "DB = 900.000, Qdrant = 740.000", the
// reconciler surfaces projection drift every tick so alerts fire while
// the drift is small. Target: projection_coverage_ratio == 1.0 and
// orphan_count == 0.
//
// This service does NOT repair anything — it only measures. Repair
// belongs to the reconcile-qdrant / reindex-qdrant admin commands
// (and the outbox worker). Mirrors the deletion-reconciler shape
// (application owns orchestration + typed ports; infrastructure owns
// concrete adapters).
package projectionreconciler

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// ProjectionParity is one reconciliation sample: the eligible-vs-active
// set parity for the active Qdrant projection.
type ProjectionParity struct {
	// Collection is the active Qdrant collection name that was checked.
	Collection string
	// EligibleSQLite is the number of assets satisfying the canonical
	// eligibility policy (SearchIndexEligibilitySQL SSOT).
	EligibleSQLite int
	// QdrantPoints is the number of points in the active collection
	// carrying a payload asset_id.
	QdrantPoints int
	// MissingCount is the number of eligible assets absent from Qdrant
	// (a projection bug).
	MissingCount int
	// OrphanCount is the number of Qdrant points whose asset_id is not
	// in the eligible set (a stale projection).
	OrphanCount int
	// PointsMissingAssetID is the number of points whose payload carries
	// no asset_id (unmatchable — counts as drift).
	PointsMissingAssetID int
	// CompleteScan is true when every point was scanned with zero errors.
	CompleteScan bool
}

// CoverageRatio returns eligible-present / eligible — the
// projection_coverage_ratio metric. 1.0 = full coverage (every eligible
// asset is present in Qdrant). Vacuous when there are no eligible
// assets: nothing to project, coverage is 1.0. Never negative: a
// missing count larger than eligible (impossible in practice) clamps
// to 0.
func (p ProjectionParity) CoverageRatio() float64 {
	if p.EligibleSQLite == 0 {
		return 1.0
	}
	present := p.EligibleSQLite - p.MissingCount
	if present < 0 {
		present = 0
	}
	return float64(present) / float64(p.EligibleSQLite)
}

// ParityChecker is the port that computes one parity sample. The
// production concrete is
// internal/infrastructure/qdrant/verification.ProjectionParityCheckerAdapter
// (wrapping the ProjectionVerifier — the same verifier used by the
// verify-projection admin command, so the periodic signal and the
// operator-facing gate share one boundary).
type ParityChecker interface {
	CheckProjectionParity(ctx context.Context) (ProjectionParity, error)
}

// Metrics is the port for the per-tick metric emission. The production
// concrete is
// internal/infrastructure/qdrant/verification.ParityMetricsAdapter
// (Prometheus gauges declared in
// internal/infrastructure/observability/metrics_media.go).
type Metrics interface {
	// ObserveParity records a successful parity sample (sets the
	// coverage/orphan/missing gauges + bumps the runs counter + stamps
	// last-success).
	ObserveParity(p ProjectionParity)
	// ObserveError records a failed tick (bumps the errors counter).
	ObserveError()
}

// noopMetrics is the test/partial-wiring fallback: zero bumps, no
// Prometheus dependency.
type noopMetrics struct{}

func (noopMetrics) ObserveParity(ProjectionParity) {}
func (noopMetrics) ObserveError()                   {}

// Service is the periodic projection-parity reconciler. Construction
// via NewServiceFromDeps.
type Service struct {
	checker  ParityChecker
	metrics  Metrics
	interval time.Duration
	log      *zap.Logger
}

// ServiceDeps bundles the injectable ports.
//
//	Required (panic if nil at construction):
//	  - Checker
//	Optional (fall back to defaults):
//	  - Metrics  → noopMetrics
//	  - Interval → 15m
//	  - Log      → zap.NewNop
type ServiceDeps struct {
	Checker  ParityChecker
	Metrics  Metrics
	Interval time.Duration
	Log      *zap.Logger
}

// NewServiceFromDeps constructs a Service. nil Checker PANICS at
// construction — a half-wired reconciler silently no-opping its tick
// was the canonical regression pattern this codebase guards against
// (PR-10: production half-built wiring must trip, not no-op).
func NewServiceFromDeps(deps ServiceDeps) *Service {
	if deps.Checker == nil {
		panic("projectionreconciler.NewServiceFromDeps: ServiceDeps.Checker must not be nil")
	}
	if deps.Metrics == nil {
		deps.Metrics = noopMetrics{}
	}
	if deps.Interval <= 0 {
		deps.Interval = 15 * time.Minute
	}
	if deps.Log == nil {
		deps.Log = zap.NewNop()
	}
	return &Service{
		checker:  deps.Checker,
		metrics:  deps.Metrics,
		interval: deps.Interval,
		log:      deps.Log.Named("projection-reconciler"),
	}
}

// Run drives the periodic ticker loop. The first tick runs immediately
// (no interval delay), then every interval until ctx is cancelled.
// Idempotent re-entry: the loop is stateless across ticks; at most one
// active Run per Service is supported.
func (s *Service) Run(ctx context.Context) {
	s.log.Info("projection-reconciler: started",
		zap.Duration("interval", s.interval),
	)
	t := time.NewTicker(s.interval)
	defer t.Stop()

	// Tick once immediately so the gauges are populated at startup.
	s.tickOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			s.log.Info("projection-reconciler: context cancelled, exiting")
			return
		case <-t.C:
			s.tickOnce(ctx)
		}
	}
}

// tickOnce invokes ReconcileOnce and swallows the error (already
// logged + metered inside ReconcileOnce) so the ticker loop stays
// simple.
func (s *Service) tickOnce(ctx context.Context) {
	_ = s.ReconcileOnce(ctx)
}

// ReconcileOnce computes one parity sample and emits the metrics.
// Metrics are ALWAYS emitted: on success ObserveParity (gauges +
// runs + last-success), on failure ObserveError (errors counter) —
// dashboards see both "the reconciler ran" and "the reconciler hit an
// error" signals independently. Returns the error (if any) for
// testability.
func (s *Service) ReconcileOnce(ctx context.Context) error {
	parity, err := s.checker.CheckProjectionParity(ctx)
	if err != nil {
		s.metrics.ObserveError()
		s.log.Error("projection-reconciler: parity check failed",
			zap.Error(err),
		)
		return err
	}
	s.metrics.ObserveParity(parity)
	s.log.Info("projection-reconciler: parity sampled",
		zap.String("collection", parity.Collection),
		zap.Int("eligible_sqlite", parity.EligibleSQLite),
		zap.Int("qdrant_points", parity.QdrantPoints),
		zap.Int("missing", parity.MissingCount),
		zap.Int("orphan", parity.OrphanCount),
		zap.Int("points_without_asset_id", parity.PointsMissingAssetID),
		zap.Bool("complete_scan", parity.CompleteScan),
		zap.Float64("coverage_ratio", parity.CoverageRatio()),
	)
	return nil
}
