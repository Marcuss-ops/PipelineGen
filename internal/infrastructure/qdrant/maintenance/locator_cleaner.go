// Package qdrant — locator payload cleaner (QDRANT-005, June 2026).
//
// LocatorCleaner scrolls every point in a Qdrant collection and strips
// legacy payload keys (drive_link, local_path) that were written by
// pre-QDRANT-001 upsert paths. New upserts (via BuildPayload) no longer
// emit these keys, but historical points in existing collections still
// carry them. This cleaner provides an idempotent, dry-run-first scrub
// so operators can verify the scope before mutating.
//
// Design:
//   - Scrolls the collection via transport.Client.ScrollPoints (with_payload=true).
//   - Identifies points whose payload contains "drive_link" or "local_path".
//   - In dry-run mode, only counts and reports — zero mutations.
//   - In apply mode, batch-deletes the keys via transport.Client.DeletePayloadKeys.
//   - Idempotent: running twice produces zero affected points on the second
//     pass (the keys are already gone).
//   - Does NOT touch vectors, other payload fields, or the SQLite database.
//
// P4 PREALLOC-CLEANER (July 2026): affectedIDs is pre-allocated using a
// two-tier estimate: (1) CountPoints gives an upper-bound capacity before
// the first scroll, (2) after the first scroll page the actual affected
// ratio refines the estimate. This eliminates reallocation churn on
// collections with a high proportion of legacy points.
//
// Usage:
//
//	cleaner := qdrant.NewLocatorCleaner(client, schema, log)
//	report, err := cleaner.CleanLocators(ctx, false) // dry-run
//	report, err := cleaner.CleanLocators(ctx, true)  // apply
package maintenance

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

// LocatorCleaner strips legacy drive_link and local_path payload keys
// from Qdrant points. It consumes the transport.Client + schema.IndexSchema to resolve
// the active collection via the runtime alias.
type LocatorCleaner struct {
	client *transport.Client
	schema *schema.IndexSchema
	log    *zap.Logger
}

// NewLocatorCleaner creates a LocatorCleaner.
func NewLocatorCleaner(client *transport.Client, schema *schema.IndexSchema, log *zap.Logger) *LocatorCleaner {
	return &LocatorCleaner{
		client: client,
		schema: schema,
		log:    log,
	}
}

// legacyKeys are the payload keys that BuildPayload no longer writes
// (QDRANT-001 closure) but legacy upserts left behind.
var legacyKeys = []string{"drive_link", "local_path"}

// scrollBatchSize controls how many points are fetched per scroll page.
const scrollBatchSize = 250

// deleteBatchSize controls how many point IDs are sent per
// DeletePayloadKeys call (the Qdrant API has a practical limit).
const deleteBatchSize = 200

// CleanLocators scans the active Qdrant collection for legacy
// drive_link / local_path payload keys. In dry-run mode it only reports
// counts; with apply=true it strips the keys via DeletePayloadKeys.
//
// P4 PREALLOC-CLEANER (July 2026): affectedIDs is pre-allocated before
// the scroll using CountPoints as an upper bound. After the first scroll
// page, the observed affected-ratio refines the estimate. firstPageAffected
// is tracked inline (no redundant iteration). When CountPoints fails the
// slice starts nil — Go's append doubling handles it gracefully.
func (c *LocatorCleaner) CleanLocators(ctx context.Context, apply bool) (*schema.LocatorCleanupReport, error) {
	collection, err := c.client.GetAliasTarget(ctx, c.schema.RuntimeAlias)
	if err != nil {
		return nil, fmt.Errorf("resolve alias target: %w", err)
	}
	if collection == "" {
		return nil, fmt.Errorf("runtime alias %q has no target — run EnsureSchema first", c.schema.RuntimeAlias)
	}

	report := &schema.LocatorCleanupReport{
		DryRun:     !apply,
		Collection: collection,
	}

	// ── P4: Estimate affected count for pre-allocation ──────────
	totalPoints, countErr := c.client.CountPoints(ctx, collection)
	if countErr != nil {
		c.log.Warn("locator-cleaner: CountPoints failed, falling back to no pre-alloc",
			zap.String("collection", collection),
			zap.Error(countErr))
		totalPoints = 0
	}

	affectedEstimate := totalPoints
	if affectedEstimate > 0 {
		c.log.Debug("locator-cleaner: pre-alloc from CountPoints",
			zap.Int("total_points", totalPoints))
	}

	// Pre-allocate before the scroll loop so the first page doesn't
	// waste appends on a starting-nil backing array.
	var affectedIDs []string
	if affectedEstimate > 0 {
		affectedIDs = make([]string, 0, affectedEstimate)
	}

	firstPageDone := false
	firstPageAffected := 0
	offset := ""

	for {
		page, err := c.client.ScrollPoints(ctx, collection, offset, scrollBatchSize, nil)
		if err != nil {
			report.AllocCapacity = cap(affectedIDs)
			return report, fmt.Errorf("scroll %q at offset %q (scrolled %d points so far): %w",
				collection, offset, report.TotalPointsScrolled, err)
		}

		for _, pt := range page.Points {
			report.TotalPointsScrolled++
			hasDriveLink := hasKey(pt.Payload, "drive_link")
			hasLocalPath := hasKey(pt.Payload, "local_path")

			if hasDriveLink {
				report.PointsWithDriveLink++
			}
			if hasLocalPath {
				report.PointsWithLocalPath++
			}
			if hasDriveLink || hasLocalPath {
				report.PointsAffected++
				if !firstPageDone {
					firstPageAffected++
				}
				affectedIDs = append(affectedIDs, pt.ID)
			}
		}

		// ── P4: Refine estimate after first page ────────────────
		if !firstPageDone && totalPoints > 0 && len(page.Points) > 0 {
			firstPageDone = true

			if firstPageAffected > 0 {
				ratio := float64(firstPageAffected) / float64(len(page.Points))
				estimated := int(float64(totalPoints) * ratio)
				if estimated < report.PointsAffected {
					estimated = report.PointsAffected
				}
				if estimated > totalPoints {
					estimated = totalPoints
				}

				// Re-allocate only if the refined estimate is
				// materially larger than current capacity.
				if cap(affectedIDs) < estimated {
					resized := make([]string, len(affectedIDs), estimated)
					copy(resized, affectedIDs)
					affectedIDs = resized
				}
				c.log.Debug("locator-cleaner: refined pre-alloc from first-page ratio",
					zap.Int("first_page_size", len(page.Points)),
					zap.Int("first_page_affected", firstPageAffected),
					zap.Float64("ratio", ratio),
					zap.Int("estimated", estimated),
					zap.Int("capacity", cap(affectedIDs)))
			}
		}

		c.log.Debug("locator-cleaner scroll page",
			zap.Int("page_size", len(page.Points)),
			zap.Int("total_scrolled", report.TotalPointsScrolled),
			zap.Int("affected_so_far", report.PointsAffected))

		if page.NextOffset == "" {
			break
		}
		offset = page.NextOffset
	}

	// Reaching this point means every scroll page completed and Qdrant
	// returned an empty cursor. Mark the report complete only now; any
	// scroll error returned above leaves it false and consumers must fail
	// closed.
	report.CompleteScan = true

	// In dry-run mode, report only — no mutations.
	if !apply {
		report.AllocCapacity = cap(affectedIDs)
		c.log.Info("locator-cleaner dry-run complete",
			zap.Int("total_scrolled", report.TotalPointsScrolled),
			zap.Int("affected", report.PointsAffected),
			zap.Int("alloc_capacity", report.AllocCapacity))
		return report, nil
	}

	// Apply: batch-delete the legacy keys.
	if len(affectedIDs) == 0 {
		c.log.Info("locator-cleaner: zero affected points, nothing to clean")
		return report, nil
	}

	for i := 0; i < len(affectedIDs); i += deleteBatchSize {
		end := i + deleteBatchSize
		if end > len(affectedIDs) {
			end = len(affectedIDs)
		}
		batch := affectedIDs[i:end]

		if err := c.client.DeletePayloadKeys(ctx, collection, legacyKeys, batch); err != nil {
			report.Errors = append(report.Errors,
				fmt.Sprintf("delete batch [%d:%d]: %v", i, end, err))
			c.log.Warn("locator-cleaner: delete batch failed",
				zap.Int("batch_start", i),
				zap.Int("batch_end", end),
				zap.Error(err))
			continue
		}
		report.BatchCount++
		report.KeysRemoved += len(batch) * len(legacyKeys)
	}

	report.AllocCapacity = cap(affectedIDs)
	c.log.Info("locator-cleaner apply complete",
		zap.Int("total_scrolled", report.TotalPointsScrolled),
		zap.Int("affected", report.PointsAffected),
		zap.Int("keys_removed", report.KeysRemoved),
		zap.Int("batch_count", report.BatchCount),
		zap.Int("error_count", len(report.Errors)),
		zap.Int("alloc_capacity", report.AllocCapacity))

	if len(report.Errors) > 0 {
		return report, fmt.Errorf("locator cleanup completed with %d batch errors", len(report.Errors))
	}
	return report, nil
}

// hasKey returns true if the payload map contains the given key
// (regardless of the value). A legacy key present but empty is
// still eligible for cleanup.
func hasKey(payload map[string]any, key string) bool {
	if payload == nil {
		return false
	}
	_, ok := payload[key]
	return ok
}
