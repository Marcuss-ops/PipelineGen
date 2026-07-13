// internal/application/qdrant/maintenance/repair-locators.go — repair-locators mode handler.
//
// Strips legacy drive_link / local_path payload keys from all points in the
// active Qdrant collection via the canonical LocatorCleaner. Fast path:
// does NOT require the full composition root (no SQLite open, no
// app.InitComposition) — only the Qdrant client.
//
// FASE 1.2 PR-GODOBJ-12 closure (2026-07-04): verbatim migration from
// cmd/admin/qdrant_maintenance_repair_locators.go per godlike/06 SSOT.
// Service.Repair delegates to the injected QdrantCleaner port (defined in
// service.go). The cmd/admin layer no longer imports internal/infrastructure/qdrant
// directly for repair — it goes through the canonical QdrantCleaner port.
package maintenance

import (
	"context"

	"go.uber.org/zap"
)

// RepairOptions is the typed-input envelope for Service.Repair.
//
//   - JSON: machine-readable JSON output (vs human-readable)
type RepairOptions struct {
	JSON bool
}

// LocatorCleanupReport is the canonical report envelope returned by the
// QdrantCleaner port.
//
// FASE 1.2 PR-GODOBJ-12 honest scope-lock: the canonical concrete type
// lives in internal/infrastructure/qdrant (locatorCleaner.CleanLocators).
// The application-layer Service.Repair only needs a structural projection
// of the report fields (collection + counts + errors); the projection
// holds a *LocatorCleanupReport pointer so the concrete type is the
// canonical SSOT surface (godlike/06 one-owner-per-fact).
//
// We mirror the field shape here so the application-layer can format the
// report without importing internal/infrastructure/qdrant directly; the
// concrete value is fed by the QdrantCleaner adapter at composition time.
type LocatorCleanupReport struct {
	Collection          string
	TotalPointsScrolled int
	PointsWithDriveLink int
	PointsWithLocalPath int
	PointsAffected      int
	KeysRemoved         int
	BatchCount          int
	Errors              []string
}

// Service.Repair handles the `repair-locators` mode of qdrant-maintenance.
// Pre-conditions:
//   - cfg.Qdrant.Enabled = true
//   - s.cleaner is non-nil (QdrantCleaner port injected at composition time)
func (s *Service) Repair(ctx context.Context, opts RepairOptions) error {
	s.log.Info("qdrant-maintenance repair-locators: scanning for legacy drive_link / local_path keys")

	report, err := s.cleaner.CleanLocators(ctx, true)
	if err != nil {
		s.log.Error("qdrant-maintenance repair-locators failed", zap.Error(err))
		if report != nil && opts.JSON {
			if jErr := s.cli.JSON(report); jErr != nil {
				s.log.Warn("qdrant-maintenance: partial-report JSON marshal failed (dropping partial line; main err returned below)", zap.Error(jErr))
			}
		}
		return err
	}

	if opts.JSON {
		return s.cli.JSON(report)
	}

	s.cli.HumanLine("=== qdrant-maintenance repair-locators ===")
	s.cli.HumanLinef("  Collection:       %s\n", report.Collection)
	s.cli.HumanLinef("  Points scrolled:  %d\n", report.TotalPointsScrolled)
	s.cli.HumanLinef("  With drive_link:  %d\n", report.PointsWithDriveLink)
	s.cli.HumanLinef("  With local_path:  %d\n", report.PointsWithLocalPath)
	s.cli.HumanLinef("  Affected (total): %d\n", report.PointsAffected)
	s.cli.HumanLinef("  Keys removed:     %d\n", report.KeysRemoved)
	s.cli.HumanLinef("  Batch calls:      %d\n", report.BatchCount)
	if len(report.Errors) > 0 {
		s.cli.HumanLinef("  Errors:           %d\n", len(report.Errors))
		for i, e := range report.Errors {
			s.cli.HumanLinef("    [%d] %s\n", i, e)
		}
	}
	s.cli.HumanLine("\nRun 'qdrant-maintenance audit' to confirm zero LegacyLocatorPayload hits.")
	return nil
}
