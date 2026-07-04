// cmd/admin/qdrant_maintenance_audit.go — audit mode handler.
//
// Dry-run: classifies all points in the active Qdrant collection over the
// 8 legacy categories, then prints the report (JSON or human-readable).
// No mutations — read-only surface. Sibling to repair-locators and
// delete-invalid (the other 2 modes in qdrant-maintenance).
package main

import (
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/legacyaudit"
)

// runQdrantMaintenanceAudit handles the `audit` mode of qdrant-maintenance.
// Pre-conditions (validated by runQdrantMaintenanceHeavy):
//   - cfg.Qdrant.Enabled = true
//   - mctx.client / mctx.activeCol / mctx.scanner are populated
//
// Pipeline: classifyForMaintenance → print report (JSON or human).
func runQdrantMaintenanceAudit(mctx *qdrantMaintContext, deps qdrantMaintDeps) error {
	report, err := classifyForMaintenance(mctx, deps)
	if err != nil {
		mctx.log.Warn("qdrant-maintenance: classify returned with errors; printing partial report", zap.Error(err))
		if deps.JSON && report != nil {
			b, _ := json.Marshal(report)
			fmt.Println(string(b))
		}
		return err
	}

	if deps.JSON {
		b, _ := json.Marshal(report)
		fmt.Println(string(b))
		return nil
	}

	fmt.Println("=== qdrant-maintenance audit ===")
	fmt.Println(legacyaudit.StringifyReport(report))
	fmt.Println("\nRe-run with 'repair-locators' to strip legacy payload keys,")
	fmt.Println("or 'delete-invalid' to dispatch canonical outbox DELETE events for non-locator findings.")
	return nil
}
