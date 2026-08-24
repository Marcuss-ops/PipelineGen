// Package stockpipeline — stock_test_helpers.go (PR-STOCK-PRODUCTION-DEPS, July 2026).
//
// Shared test helpers for the stockpipeline package. The noopRenderer
// struct is the canonical trivial StockRenderer used by tests that
// exercise the orchestrator wiring without asserting on render
// behavior. Extracted from stock_stager_wiring_test.go +
// run_upload_indexing_test.go per godlike/06 SSOT one-canonical-
// owner-per-fact: the noop stub lives ONLY in this file; test
// fixtures import it via the same-package access.
//
// godlike/07 composition-time guarantee: the orchestrator's
// composition-time fail-closed gate (orchestrator.go::RunResilient)
// rejects nil renderer with ErrOrchestratorNilDeps. Tests that
// exercise the pipeline must wire a non-nil renderer stub even if
// they don't assert on render behavior. noopRenderer satisfies the
// contract without side effects.
//
// The successNoopRenderer() function in stock_fake_availability_test.go
// is a separate helper that returns a *mapRenderer (for tests that
// might want to count calls via mapRenderer.callCount). The two
// helpers serve different purposes: noopRenderer is a type-level
// trivial impl; successNoopRenderer is a per-call configurable
// handler. Both are valid per the composition-time gate.
package assets

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// noopRenderer is a trivial StockRenderer that always returns success.
// Used by tests that exercise the orchestrator wiring without asserting
// on render behavior. The composition-time fail-closed gate
// (PR-STOCK-PRODUCTION-DEPS, July 2026) requires a non-nil renderer;
// the noop stub satisfies the contract without side effects.
type noopRenderer struct{}

var _ StockRenderer = (*noopRenderer)(nil)

// Render always returns (RenderResult{}, nil). The composition-time
// gate is satisfied; render behavior is irrelevant to the tests that
// use this stub (they assert on stage_sources / plan / extract_clips
// / publish / finalize behavior, not on render output).
func (noopRenderer) Render(_ context.Context, _ RenderRequest) (RenderResult, error) {
	return RenderResult{}, nil
}

// testLease returns a deterministic non-empty lease for stockpipeline
// tests that need the finalize step to proceed through the job-finalizer
// gate without touching the real broker wiring.
func testLease(jobID string) finalization.Lease {
	if jobID == "" {
		jobID = "stock-test-job"
	}
	return finalization.Lease{
		JobID:     jobID,
		WorkerID:  "stock-test-worker",
		LeaseID:   jobID + "-lease",
		Attempt:   1,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
}
