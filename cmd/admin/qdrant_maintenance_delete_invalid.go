// cmd/admin/qdrant_maintenance_delete_invalid.go — delete-invalid mode handler.
//
// Dispatches canonical outbox DELETE events for assets whose points hit
// non-locator legacy categories (1–6, 8). Points whose ONLY finding is
// LegacyLocatorPayload (category 7) are excluded — those are repairable
// via `repair-locators`, not deletable (per Issue 12 policy).
//
// Sibling to audit + repair-locators. Co-located helpers:
// collectNonLocatorAssetIDs + isLocatorOnly (single-caller, single-purpose,
// kept here per godlike/06 SSOT).
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/legacyaudit"
)

// runQdrantMaintenanceDeleteInvalid handles the `delete-invalid` mode of
// qdrant-maintenance.
//
// Pre-conditions (validated by runQdrantMaintenanceHeavy):
//   - cfg.Qdrant.Enabled = true
//   - mctx.client / mctx.activeCol / mctx.scanner are populated
//   - mctx.root.Outbox.Dispatcher is non-nil (validated below before apply)
func runQdrantMaintenanceDeleteInvalid(mctx *qdrantMaintContext, deps qdrantMaintDeps) error {
	report, err := classifyForMaintenance(mctx, deps)
	if err != nil {
		mctx.log.Warn("qdrant-maintenance: classify returned with errors; printing partial report", zap.Error(err))
		if deps.JSON && report != nil {
			b, _ := json.Marshal(report)
			fmt.Println(string(b))
		}
		return err
	}
	if report == nil {
		return nil
	}

	// Filter: exclude points whose ONLY finding is LegacyLocatorPayload.
	// Points with locator payload AND other categories are still included —
	// the policy is "locator-only points are repairable, not deletable".
	assetIDs := collectNonLocatorAssetIDs(report)
	if len(assetIDs) == 0 {
		if deps.JSON {
			out := map[string]any{
				"applied":    0,
				"total":      0,
				"skipped":    0,
				"audit":      report.Audit,
				"collection": report.Collection,
				"message":    "zero non-locator assets to delete — all findings are locator-only; use repair-locators",
			}
			b, _ := json.Marshal(out)
			fmt.Println(string(b))
		} else {
			fmt.Println("delete-invalid: zero non-locator assets to delete.")
			fmt.Println("(All findings are locator-only; use 'repair-locators' instead.)")
		}
		return nil
	}

	if !deps.JSON {
		fmt.Printf("=== qdrant-maintenance delete-invalid ===\n")
		fmt.Println(legacyaudit.StringifyReport(report))
		fmt.Printf("\nNon-locator assets to delete: %d\n", len(assetIDs))
		fmt.Println("\nApplying via outbox.Dispatcher.EnqueueAndDelete (the canonical deletion path; " +
			"never DELETE FROM media_assets directly)...")
	}

	if mctx.root.Outbox == nil || mctx.root.Outbox.Dispatcher == nil {
		return fmt.Errorf("apply requested but root.Outbox.Dispatcher is nil (composition root missing outbox bundle)")
	}

	if err := legacyaudit.ValidateAssetIDs(assetIDs); err != nil {
		return fmt.Errorf("apply aborted: %w", err)
	}

	applied := 0
	for _, assetID := range assetIDs {
		if strings.TrimSpace(assetID) == "" {
			continue
		}
		if err := mctx.root.Outbox.Dispatcher.EnqueueAndDelete(mctx.ctx, assetID); err != nil {
			mctx.log.Warn("outbox delete enqueue failed",
				zap.String("asset_id", assetID),
				zap.Error(err))
			continue
		}
		applied++
	}

	if deps.JSON {
		out := map[string]any{
			"applied":    applied,
			"total":      len(assetIDs),
			"skipped":    len(assetIDs) - applied,
			"audit":      report.Audit,
			"collection": report.Collection,
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
	} else {
		fmt.Printf("delete-invalid: dispatched %d canonical DELETE events to outbox_events.\n", applied)
		if applied < len(assetIDs) {
			fmt.Printf("  (%d skipped due to enqueue errors — check logs above)\n", len(assetIDs)-applied)
		}
		fmt.Println("Run `go run ./cmd/admin qdrant-readiness` afterwards to confirm dead_letters=0 and zero legacy audit hits.")
	}
	return nil
}

// collectNonLocatorAssetIDs extracts asset IDs from the audit report,
// excluding points whose ONLY finding is LegacyLocatorPayload (category 7).
// Points with locator payload AND other categories are still included —
// the policy is "locator-only points are repairable, not deletable".
func collectNonLocatorAssetIDs(r *legacyaudit.Report) []string {
	if r == nil || len(r.Points) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(r.Points))
	out := make([]string, 0, len(r.Points))
	for _, pa := range r.Points {
		if pa.AssetID == "" {
			continue
		}
		if seen[pa.AssetID] {
			continue
		}
		if isLocatorOnly(pa.Categories) {
			continue
		}
		seen[pa.AssetID] = true
		out = append(out, pa.AssetID)
	}
	return out
}

// isLocatorOnly returns true when the ONLY positive category is
// LegacyLocatorPayload — all other categories are zero. Points hitting
// locator + any other category are considered deletable.
func isLocatorOnly(c legacyaudit.Categories) bool {
	nonLocatorHits := c.NonMediaRow + c.MetadataJSON + c.HiddenTempFiles +
		c.InvalidVectors + c.WrongDimensions + c.LegacyLifecycle +
		c.NonCanonicalPointID
	return nonLocatorHits == 0 && c.LegacyLocatorPayload > 0
}
