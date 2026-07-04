// cmd/admin/qdrant_maintenance_classify.go — shared classify wrapper.
//
// Used by both audit + delete-invalid modes (the 2 modes that need the
// classification report). Sibling to repair-locators (which does NOT
// classify — it strips keys directly via LocatorCleaner).
package main

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/legacyaudit"
)

// classifyForMaintenance runs legacyaudit.Classify over the active collection
// with the configured scan limit. The scan-limit clamping (0 → 500, > 1000 → 1000)
// is delegated to newQdrantScannerAdapter's constructor (single source of truth).
//
// Pre-conditions (validated by runQdrantMaintenanceHeavy):
//   - mctx.scanner is non-nil
//   - mctx.activeCol is non-empty
//
// Returns the *legacyaudit.Report and the classify error. On partial failure,
// the caller logs the error and decides whether to print the partial report
// (audit mode: yes; delete-invalid mode: no — see the per-mode handlers).
func classifyForMaintenance(mctx *qdrantMaintContext, deps qdrantMaintDeps) (*legacyaudit.Report, error) {
	return legacyaudit.Classify(mctx.ctx, mctx.scanner, mctx.activeCol, 200)
}
