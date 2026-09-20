// Package maintenance (FASE 1.2 PR-GODOBJ-12 closure, 2026-07-04) —
// application-layer use-case orchestration for the `qdrant-maintenance` admin
// command (Issue 12, June 2026).
//
// History: this package is the canonical home for the per-mode logic that
// previously lived in cmd/admin/qdrant_maintenance_*.go sub-files. The
// mechanical split was authored via PR-GODOBJ-12; godlike/06 SSOT
// boundary: this package owns use-case orchestration (which mode to run,
// how to format the report, how to wire the dispatcher for delete-invalid);
// internal/platform/qdrant/maintenance/ continues to own the wire
// adapters (dr_adapter.go + locator_cleaner.go + reaper.go) per the
// existing PR-QDRANT-FINAL-DECISION disposition (Qdrant is LIVE on
// origin/main).
//
// File layout (4 per-capability files + 1 dispatching service):
//
//   - service.go              — Service struct + ports interface +
//     NewService constructor + per-mode dispatch
//   - audit.go                — Service.Audit (dry-run classify)
//   - repair-locators.go      — Service.Repair (strip drive_link/local_path keys)
//   - delete-invalid.go       — Service.Delete (outbox-delete non-locator assets)
//   - scanner.go              — QdrantScannerAdapter (canonical godlike/06
//     translation bridge between legacyaudit.ScrollPoint
//     and platform/qdrant.ScrollResult) +
//     classifyForMaintenance shared helper
//
// Mode set is locked at 3 (audit / repair-locators / delete-invalid) per
// the canonical policy on origin/main. A user spec referenced a 4th
// "rebuild" mode (FASE 1.2 round-1) that does NOT exist on origin/main;
// per godlike/07 no-fake-availability we ship 3 modes and document the
// honest scope-lock in the wave-tracker closure notes.
//
// godlike/06 SSOT audit pins (compile-time):
//
//	var _ legacyaudit.QdrantScanner       = (*QdrantScannerAdapter)(nil)
//	var _ legacyaudit.NextOffsetExtractor = (*QdrantScannerAdapter)(nil)
package maintenance

import (
	"context"
	"errors"

	legacyaudit "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audit"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// QdrantScannerAdapter (FASE 1.2 PR-GODOBJ-12 — verbatim migration from
// cmd/admin/qdrant_maintenance_scanner.go).
//
// Translates internal/platform/qdrant.ScrollResult into
// []legacyaudit.ScrollPoint so the application-layer audit package does
// not import the infra layer (godlike/06 SSOT: one owner per fact;
// legacyaudit owns the type, qdrant owns the wire).
//
// The adapter is single-goroutine (legacyaudit.Classify drives both
// ScrollPoints and NextOffset from one goroutine), so lastNextOffset
// is a plain field — no sync/atomic needed.
type QdrantScannerAdapter struct {
	client         *transport.Client
	activeCol      string
	pageSize       int
	lastNextOffset string
}

// ScrollPoints returns up to limit points from the next page and
// stashes the NextOffset in lastNextOffset so the classify loop's
// NextOffsetExtractor call can drive the cursor.
func (a *QdrantScannerAdapter) ScrollPoints(ctx context.Context, _ string, offset string, _ int) ([]legacyaudit.ScrollPoint, error) {
	if a.client == nil {
		return nil, errors.New("QdrantScannerAdapter: client is nil")
	}
	res, err := a.client.ScrollPoints(ctx, a.activeCol, offset, a.pageSize, nil)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	out := make([]legacyaudit.ScrollPoint, 0, len(res.Points))
	for _, p := range res.Points {
		out = append(out, legacyaudit.ScrollPoint{
			ID:      p.ID,
			Payload: p.Payload,
		})
	}
	a.lastNextOffset = res.NextOffset
	return out, nil
}

// NextOffset implements legacyaudit.NextOffsetExtractor.
func (a *QdrantScannerAdapter) NextOffset(_ []legacyaudit.ScrollPoint) string {
	return a.lastNextOffset
}

// Compile-time guards (godlike/06 audit-pin discipline: pins live next to
// the type they assert). The pre-existing `_ = sql.ErrNoRows` dead-code
// assertion that was at the bottom of the original monolithic file has
// been RETIRED per PR-QDRANT-MAINT-PER-MODE (godlike/07 minimum-blast-radius:
// the import is no longer used in any per-mode file, the speculative
// "future expansion of the apply loop" comment is not a real dependency,
// the assertion was guarding a hypothetical surface that never landed).
var (
	_ legacyaudit.QdrantScanner       = (*QdrantScannerAdapter)(nil)
	_ legacyaudit.NextOffsetExtractor = (*QdrantScannerAdapter)(nil)
)
