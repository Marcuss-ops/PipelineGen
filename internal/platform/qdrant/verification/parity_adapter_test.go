package verification

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/reconciliation/projection"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// ─── Checker adapter ────────────────────────────────────────────────

// TestParityCheckerAdapter_UnwiredVerifierFailsClosed — a checker
// wired without its verifier returns a typed error (wiring bug).
func TestParityCheckerAdapter_UnwiredVerifierFailsClosed(t *testing.T) {
	t.Parallel()
	_, err := ProjectionParityCheckerAdapter{}.CheckProjectionParity(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Verifier is nil")
}

// TestParityCheckerAdapter_MapsVerifierReport — the adapter maps the
// ProjectionVerifier report onto the application ProjectionParity.
func TestParityCheckerAdapter_MapsVerifierReport(t *testing.T) {
	t.Parallel()
	srv := mockProjectionQdrant(t, projectionMockSpec{
		AliasTarget: "media_assets_v4_test",
		Pages: [][]map[string]any{
			{point("asset-1"), point("asset-2")},
		},
	})
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	verifier := NewProjectionVerifier(newClientAt(srv.URL), &stubAssetStore{ids: []string{"asset-1", "asset-2"}}, schema, zap.NewNop())
	checker := ProjectionParityCheckerAdapter{Verifier: verifier}

	parity, err := checker.CheckProjectionParity(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "media_assets_v4_test", parity.Collection)
	assert.Equal(t, 2, parity.EligibleSQLite)
	assert.Equal(t, 2, parity.QdrantPoints)
	assert.Equal(t, 0, parity.MissingCount)
	assert.Equal(t, 0, parity.OrphanCount)
	assert.True(t, parity.CompleteScan)
	assert.Equal(t, 1.0, parity.CoverageRatio())
}

// TestParityCheckerAdapter_PropagatesDrift — missing/orphan surface in
// the application parity (the periodic signal a dashboard alerts on).
func TestParityCheckerAdapter_PropagatesDrift(t *testing.T) {
	t.Parallel()
	srv := mockProjectionQdrant(t, projectionMockSpec{
		AliasTarget: "media_assets_v4_test",
		Pages: [][]map[string]any{
			{point("asset-1"), point("asset-9")},
		},
	})
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	verifier := NewProjectionVerifier(newClientAt(srv.URL), &stubAssetStore{ids: []string{"asset-1", "asset-2"}}, schema, zap.NewNop())
	checker := ProjectionParityCheckerAdapter{Verifier: verifier}

	parity, err := checker.CheckProjectionParity(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, parity.MissingCount)
	assert.Equal(t, 1, parity.OrphanCount)
	assert.Equal(t, 0.5, parity.CoverageRatio())
}

// ─── Metrics adapter ────────────────────────────────────────────────

// TestParityMetricsAdapter_SetsGauges — ObserveParity writes the
// projection gauges + bumps the runs counter + stamps last success;
// ObserveError bumps the errors counter.
func TestParityMetricsAdapter_SetsGauges(t *testing.T) {
	t.Parallel()
	parity := projectionreconciler.ProjectionParity{
		Collection:           "media_assets_v4_test",
		EligibleSQLite:       527,
		QdrantPoints:         527,
		MissingCount:         0,
		OrphanCount:          0,
		PointsMissingAssetID: 0,
		CompleteScan:         true,
	}

	runsBefore := testutil.ToFloat64(observability.ProjectionReconcileRunsTotal)
	adapter := ParityMetricsAdapter{}
	adapter.ObserveParity(parity)

	assert.Equal(t, 1.0, testutil.ToFloat64(observability.ProjectionCoverageRatio))
	assert.Equal(t, 0.0, testutil.ToFloat64(observability.ProjectionOrphanCount))
	assert.Equal(t, 0.0, testutil.ToFloat64(observability.ProjectionMissingCount))
	assert.Equal(t, 527.0, testutil.ToFloat64(observability.ProjectionEligibleSQLite))
	assert.Equal(t, 527.0, testutil.ToFloat64(observability.ProjectionQdrantPoints))
	assert.Equal(t, 1.0, testutil.ToFloat64(observability.ProjectionScanComplete))
	assert.Equal(t, runsBefore+1, testutil.ToFloat64(observability.ProjectionReconcileRunsTotal))
	assert.Greater(t, testutil.ToFloat64(observability.ProjectionReconcileLastSuccess), 0.0)

	// Drift sample: coverage drops, orphans appear, scan complete flag.
	drift := projectionreconciler.ProjectionParity{
		EligibleSQLite: 527,
		QdrantPoints:   500,
		MissingCount:   27,
		OrphanCount:    3,
		CompleteScan:   false,
	}
	adapter.ObserveParity(drift)
	assert.InDelta(t, 500.0/527.0, testutil.ToFloat64(observability.ProjectionCoverageRatio), 1e-9)
	assert.Equal(t, 3.0, testutil.ToFloat64(observability.ProjectionOrphanCount))
	assert.Equal(t, 27.0, testutil.ToFloat64(observability.ProjectionMissingCount))
	assert.Equal(t, 0.0, testutil.ToFloat64(observability.ProjectionScanComplete))

	// Error path.
	errsBefore := testutil.ToFloat64(observability.ProjectionReconcileErrorsTotal)
	adapter.ObserveError()
	assert.Equal(t, errsBefore+1, testutil.ToFloat64(observability.ProjectionReconcileErrorsTotal))
}
