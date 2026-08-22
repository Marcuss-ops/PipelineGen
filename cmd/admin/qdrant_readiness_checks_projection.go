// cmd/admin/qdrant_readiness_checks_projection.go — eligibility-vs-
// projection parity readiness check (plan item #14, August 2026).
//
// The readiness gate must NOT compare COUNT(index_state='INDEXED') vs
// COUNT(Qdrant): INDEXED is an observed projection result, while
// eligibility is a property of the canonical asset row. The correct
// comparison is SQLiteEligibleAssetIDs (SearchIndexEligibilitySQL
// SSOT) vs QdrantActiveAssetIDs — PASS only when 0 missing and 0
// orphan (plus a complete scan).
//
// Reuses the ProjectionVerifier — the same verifier behind the
// verify-projection admin command and the periodic projection
// reconciler — so the operator gate, the CLI check and the periodic
// signal share ONE boundary.
package main

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/indexing"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/verification"
)

// runProjectionParity runs the active-projection parity verifier with
// the readiness dependency bag. Guards mirror checkQdrantActiveCollection.
func runProjectionParity(ctx context.Context, deps readinessDeps) (*verification.ProjectionVerificationReport, error) {
	if deps.Cfg == nil || deps.Cfg.Qdrant.BaseURL == "" {
		return nil, fmt.Errorf("qdrant.base_url is empty")
	}
	if !deps.Cfg.Qdrant.Enabled {
		return nil, fmt.Errorf("qdrant.enabled=false (projection parity requires qdrant.enabled=true)")
	}
	if deps.DB == nil {
		return nil, fmt.Errorf("raw *sql.DB is nil — cannot load eligible SQLite asset IDs")
	}
	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: deps.Cfg.Qdrant.BaseURL,
		Timeout: deps.Cfg.Qdrant.Timeout,
		APIKey:  deps.Cfg.Qdrant.APIKey,
	}, deps.Log)
	verifier := verification.NewProjectionVerifier(
		client,
		indexing.NewSQLiteAssetStore(deps.DB),
		qdrantschema.DefaultV3Schema(),
		deps.Log,
	)
	return verifier.VerifyActiveProjection(ctx)
}

// checkProjectionParity is the readiness check: PASS only when every
// eligible SQLite asset is present in the active Qdrant projection
// (0 missing) AND no stale points exist (0 orphan) AND the scan
// completed cleanly.
func checkProjectionParity(ctx context.Context, deps readinessDeps) checkStatus {
	report, err := runProjectionParity(ctx, deps)
	if err != nil {
		return checkStatus{Err: "projection parity check failed: " + err.Error()}
	}
	if !report.Passed {
		return checkStatus{Err: fmt.Sprintf(
			"projection parity: eligible_sqlite=%d qdrant_points=%d missing_in_qdrant=%d orphan_in_qdrant=%d points_without_asset_id=%d complete_scan=%v (PASS requires 0 missing, 0 orphan, complete scan)",
			report.EligibleSQLite, report.QdrantPoints, report.MissingCount,
			report.OrphanCount, report.PointsMissingAssetID, report.CompleteScan,
		)}
	}
	return checkStatus{Pass: true}
}

// probeProjectionParity is the orchestrator-side helper: runs the
// parity verifier ONCE, populates the report's parity fields and the
// projection_parity check entry. Mirrors qdrantProbeAndSchema (the
// generic check loop then skips the pre-set key).
func probeProjectionParity(ctx context.Context, deps readinessDeps, report *qdrantReadinessReport) {
	parity, err := runProjectionParity(ctx, deps)
	if err != nil {
		report.Checks["projection_parity"] = "fail"
		return
	}
	report.ProjectionEligibleSQLite = parity.EligibleSQLite
	report.ProjectionQdrantPoints = parity.QdrantPoints
	report.ProjectionMissingCount = parity.MissingCount
	report.ProjectionOrphanCount = parity.OrphanCount
	if parity.Passed {
		report.Checks["projection_parity"] = "pass"
	} else {
		report.Checks["projection_parity"] = "fail"
	}
}
