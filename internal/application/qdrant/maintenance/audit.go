// internal/application/qdrant/maintenance/audit.go — audit mode handler.
//
// Dry-run: classifies all points in the active Qdrant collection over the
// 8 legacy categories, then prints the report (JSON or human-readable).
// No mutations — read-only surface. Sibling to repair-locators and
// delete-invalid (the other 2 modes in qdrant-maintenance).
//
// FASE 1.2 PR-GODOBJ-12 closure (2026-07-04): verbatim migration from
// cmd/admin/qdrant_maintenance_audit.go per godlike/06 SSOT — the
// application layer is the canonical owner of use-case orchestration.
//
// Compile-drift fixup (2026-07-04, post-review): replace local helper
// `reportString(report interface{Stringify() string})` with direct
// `legacyaudit.StringifyReport(report)` package-function call (the
// canonical stringifier is a pkg-level func, not a Report method).
// Also: remove the now-unused `interface{...}` happy-indirection that
// would drift from the canonical func signature.
package maintenance

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/legacyaudit"
)

// AuditOptions is the typed-input envelope for the Service.Audit method.
// Mirrors the qdrantMaintDeps fields needed by the audit pipeline:
//
//   - JSON: machine-readable JSON output (vs human-readable)
//   - Limit: optional cap on per-page scan size (Qdrant max=1000; 0 = default 500)
type AuditOptions struct {
	JSON  bool
	Limit int
}

// Service.Audit handles the `audit` mode of qdrant-maintenance.
//
// Pre-conditions (validated by Service.initHeavy before this method is called):
//   - cfg.Qdrant.Enabled = true
//   - s.client / s.activeCol / s.scanner are populated
//   - ctx is non-nil
//
// Pipeline: classifyForMaintenance → print report (JSON or human).
func (s *Service) Audit(ctx context.Context, opts AuditOptions) error {
	report, err := classifyForMaintenance(ctx, s.scanner, s.activeCol, opts.Limit)
	if err != nil {
		s.log.Warn("qdrant-maintenance: classify returned with errors; printing partial report", zap.Error(err))
		if opts.JSON && report != nil {
			b, _ := json.Marshal(report)
			fmt.Println(string(b))
		}
		return err
	}

	if opts.JSON {
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
