// internal/platform/qdrant/maintenance/delete-invalid.go — delete-invalid mode handler.
//
// Dispatches canonical outbox DELETE events for assets whose points hit
// non-locator legacy categories (1–6, 8). Points whose ONLY finding is
// LegacyLocatorPayload (category 7) are excluded — those are repairable
// via `repair-locators`, not deletable (per Issue 12 policy).
//
// FASE 1.2 PR-GODOBJ-12 closure (2026-07-04): verbatim migration from
// cmd/admin/qdrant_maintenance_delete_invalid.go per godlike/06 SSOT.
// The OutboxDispatcher port (defined in service.go) replaces the direct
// mctx.root.Outbox.Dispatcher import so the application-layer code
// stays decoupled from internal/app (composition root).
//
// Compile-drift fixup (2026-07-04, post-review): replace local helper
// `reportString(report interface{Stringify() string})` with direct
// `legacyaudit.StringifyReport(report)` package-function call.
package maintenance

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/legacyaudit"
)

// DeleteOptions is the typed-input envelope for Service.Delete.
//
//   - JSON: machine-readable JSON output (vs human-readable)
//   - Limit: optional cap on per-page scan size (Qdrant max=1000; 0 = default 500)
type DeleteOptions struct {
	JSON  bool
	Limit int
}

// Service.Delete handles the `delete-invalid` mode of qdrant-maintenance.
//
// Pre-conditions (validated by Service.initHeavy before this method is called):
//   - cfg.Qdrant.Enabled = true
//   - s.scanner + s.activeCol populated (via classifyForMaintenance)
//   - s.dispatcher is non-nil (OutboxDispatcher port injected at composition time)
func (s *Service) Delete(ctx context.Context, opts DeleteOptions) error {
	report, err := classifyForMaintenance(ctx, s.scanner, s.activeCol, opts.Limit)
	if err != nil {
		s.log.Warn("qdrant-maintenance: classify returned with errors; printing partial report", zap.Error(err))
		if opts.JSON && report != nil {
			if jErr := s.cli.JSON(report); jErr != nil {
				s.log.Warn("qdrant-maintenance: partial-report JSON marshal failed (dropping partial line; main err returned below)", zap.Error(jErr))
			}
		}
		return err
	}
	if report == nil {
		return nil
	}
	if !report.CompleteScan {
		return fmt.Errorf("delete-invalid aborted: audit report is incomplete; no deletion events were dispatched")
	}

	// Filter: exclude points whose ONLY finding is LegacyLocatorPayload.
	// Points with locator payload AND other categories are still included —
	// the policy is "locator-only points are repairable, not deletable".
	assetIDs := collectNonLocatorAssetIDs(report)
	if len(assetIDs) == 0 {
		if opts.JSON {
			out := map[string]any{
				"applied":    0,
				"total":      0,
				"skipped":    0,
				"audit":      report.Audit,
				"collection": report.Collection,
				"message":    "zero non-locator assets to delete — all findings are locator-only; use repair-locators",
			}
			return s.cli.JSON(out)
		}
		s.cli.HumanLine("delete-invalid: zero non-locator assets to delete.")
		s.cli.HumanLine("(All findings are locator-only; use 'repair-locators' instead.)")
		return nil
	}

	if !opts.JSON {
		s.cli.HumanLine("=== qdrant-maintenance delete-invalid ===")
		s.cli.HumanLine(legacyaudit.StringifyReport(report))
		s.cli.HumanLinef("\nNon-locator assets to delete: %d\n", len(assetIDs))
		s.cli.HumanLine("\nApplying via outbox.Dispatcher.EnqueueAndDelete (the canonical deletion path; " +
			"never DELETE FROM media_assets directly)...")
	}

	if s.dispatcher == nil {
		return fmt.Errorf("apply requested but dispatcher is nil (composition root missing outbox bundle)")
	}

	if err := legacyaudit.ValidateAssetIDs(assetIDs); err != nil {
		return fmt.Errorf("apply aborted: %w", err)
	}

	applied := 0
	for _, assetID := range assetIDs {
		if strings.TrimSpace(assetID) == "" {
			continue
		}
		if err := s.dispatcher.EnqueueAndDelete(ctx, assetID); err != nil {
			s.log.Warn("outbox delete enqueue failed",
				zap.String("asset_id", assetID),
				zap.Error(err))
			continue
		}
		applied++
	}

	if opts.JSON {
		out := map[string]any{
			"applied":    applied,
			"total":      len(assetIDs),
			"skipped":    len(assetIDs) - applied,
			"audit":      report.Audit,
			"collection": report.Collection,
		}
		return s.cli.JSON(out)
	}
	s.cli.HumanLinef("delete-invalid: dispatched %d canonical DELETE events to outbox_events.\n", applied)
	if applied < len(assetIDs) {
		s.cli.HumanLinef("  (%d skipped due to enqueue errors — check logs above)\n", len(assetIDs)-applied)
	}
	s.cli.HumanLine("Run `go run ./cmd/admin qdrant-readiness` afterwards to confirm dead_letters=0 and zero legacy audit hits.")
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
