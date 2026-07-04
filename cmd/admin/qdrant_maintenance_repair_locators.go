// cmd/admin/qdrant_maintenance_repair_locators.go — repair-locators mode handler.
//
// Strips legacy drive_link / local_path payload keys from all points in the
// active Qdrant collection via the canonical LocatorCleaner. Fast path:
// does NOT require the full composition root (no SQLite open, no
// app.InitComposition) — only the Qdrant client.
package main

import (
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// runQdrantMaintenanceRepairLocators handles the `repair-locators` mode of
// qdrant-maintenance. Renamed from runRepairLocators per PR-QDRANT-MAINT-PER-MODE
// (godlike/06 SSOT: one canonical naming convention across all 3 modes).
//
// Pre-conditions (validated by runQdrantMaintenance in the orchestrator):
//   - cfg.Qdrant.Enabled = true
//   - mctx.client / mctx.activeCol may be nil (not used in this fast path)
func runQdrantMaintenanceRepairLocators(mctx *qdrantMaintContext, deps qdrantMaintDeps) error {
	mctx.log.Info("qdrant-maintenance repair-locators: scanning for legacy drive_link / local_path keys")

	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: mctx.cfg.Qdrant.BaseURL,
		APIKey:  mctx.cfg.Qdrant.APIKey,
		Timeout: mctx.cfg.Qdrant.Timeout,
	}, mctx.log)
	schema := qdrant.DefaultV3Schema()
	cleaner := qdrant.NewLocatorCleaner(client, schema, mctx.log)

	report, err := cleaner.CleanLocators(mctx.ctx, true)
	if err != nil {
		mctx.log.Error("qdrant-maintenance repair-locators failed", zap.Error(err))
		if report != nil && deps.JSON {
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

	fmt.Println("=== qdrant-maintenance repair-locators ===")
	fmt.Printf("  Collection:       %s\n", report.Collection)
	fmt.Printf("  Points scrolled:  %d\n", report.TotalPointsScrolled)
	fmt.Printf("  With drive_link:  %d\n", report.PointsWithDriveLink)
	fmt.Printf("  With local_path:  %d\n", report.PointsWithLocalPath)
	fmt.Printf("  Affected (total): %d\n", report.PointsAffected)
	fmt.Printf("  Keys removed:     %d\n", report.KeysRemoved)
	fmt.Printf("  Batch calls:      %d\n", report.BatchCount)
	if len(report.Errors) > 0 {
		fmt.Printf("  Errors:           %d\n", len(report.Errors))
		for i, e := range report.Errors {
			fmt.Printf("    [%d] %s\n", i, e)
		}
	}
	fmt.Println("\nRun 'qdrant-maintenance audit' to confirm zero LegacyLocatorPayload hits.")
	return nil
}
