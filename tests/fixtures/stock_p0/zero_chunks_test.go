// Package stock_p0 — zero_chunks regression-guard fixture (Stock Cutover §12-1).
//
// Stock Cutover §12-1 P0 2.1 — zero chunks finalized → job FAILED.
//
// This fixture pins the rule at the FAIL-CLOSED GATE surface:
//
//	stockpipeline.VerifyChunks(<empty>) MUST raise ErrStockNoChunksFinalized
//
// The fixture exercises the gate directly (pure function — no Service /
// Orchestrator / job-broker dependency, no async lifecycle). The Stock
// §12-1 contract is "no silent success" — if a run produces zero chunks,
// the orchestrator MUST surface this gate error to drive the job ledger
// to a non-SUCCEEDED terminal state.
//
// RED today (June 2026): production already honors this contract via the
// Stock Cutover §12-1 fail-closed gate hardening. The fixture is GREEN
// today and serves as the regression guard for future refactors that
// might re-introduce the silent-success window.
//
// See also: stockpipeline.ErrStockNoChunksFinalized (production sentinel,
// godlike/07 typed-error contract).
package stock_p0

import (
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
)

func TestP0_ZeroChunks_NilSliceRaisesErrStockNoChunksFinalized(t *testing.T) {
	err := stockpipeline.VerifyChunks(nil)
	if !errors.Is(err, stockpipeline.ErrStockNoChunksFinalized) {
		t.Fatalf("RED: VerifyChunks(nil) must return ErrStockNoChunksFinalized, got %v", err)
	}
}

func TestP0_ZeroChunks_EmptySliceRaisesErrStockNoChunksFinalized(t *testing.T) {
	err := stockpipeline.VerifyChunks([]stockpipeline.ChunkState{})
	if !errors.Is(err, stockpipeline.ErrStockNoChunksFinalized) {
		t.Fatalf("RED: VerifyChunks([]) must return ErrStockNoChunksFinalized, got %v", err)
	}
}
