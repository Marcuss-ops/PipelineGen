// Package qdrant — P1 QDRANT-VERIFIER-SPLIT: counts phase (July 2026).
//
// verifyCounts runs the point-count parity check against the Qdrant
// collection and loads the SQLite asset IDs for downstream missing/orphan
// computation. Extracted from verifier.go's VerifyReindex so the
// orchestrator stays thin.
package verification

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// verifyCounts performs Gate 1 (point count parity) and loads the
// SQLite asset ID set for the missing/orphan comparison.
//
// Returns the sqliteSet (map of asset IDs from SQLite), or nil on
// fatal error. Populates report.ActualPoints and report.Errors.
func (v *ReindexVerifier) verifyCounts(ctx context.Context, target string, expected int, report *schema.SwitchReport) (sqliteSet map[string]bool, err error) {
	// ── Gate 1: schema.Point count parity ──────────────────────────────
	if err := v.verifyPointCountParity(ctx, target, expected, report); err != nil {
		return nil, err
	}

	// ── Load SQLite IDs for missing/orphan computation ──────────
	sqliteIDs, err := v.assetStore.ListAllAssetIDs(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("list SQLite asset IDs: %v", err))
		return nil, fmt.Errorf("QDRANT-003: cannot verify reindex — SQLite list failed: %w", err)
	}
	sqliteSet = make(map[string]bool, len(sqliteIDs))
	for _, id := range sqliteIDs {
		sqliteSet[id] = true
	}
	return sqliteSet, nil
}

// verifyPointCountParity checks that the Qdrant collection's point count
// exactly matches the expected count. Strict equality (PR 12): both
// under-count and over-count are blocking. The mismatch is appended to
// report.Errors but does NOT return an error — the verifier continues
// gathering diagnostics. Returns error only on a fatal CountPoints
// failure (Qdrant unreachable).
func (v *ReindexVerifier) verifyPointCountParity(ctx context.Context, target string, expected int, report *schema.SwitchReport) error {
	actual, err := v.client.CountPoints(ctx, target)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("count points: %v", err))
		return fmt.Errorf("QDRANT-003: cannot verify reindex — count failed: %w", err)
	}
	report.ActualPoints = actual

	if actual != expected {
		report.Errors = append(report.Errors,
			fmt.Sprintf("PR 12 point count mismatch (strict): expected %d, actual %d (delta %+d)",
				expected, actual, actual-expected))
	}
	return nil
}
