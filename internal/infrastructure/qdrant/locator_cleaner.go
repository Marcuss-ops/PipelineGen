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
//   - Scrolls the collection via Client.ScrollPoints (with_payload=true).
//   - Identifies points whose payload contains "drive_link" or "local_path".
//   - In dry-run mode, only counts and reports — zero mutations.
//   - In apply mode, batch-deletes the keys via Client.DeletePayloadKeys.
//   - Idempotent: running twice produces zero affected points on the second
//     pass (the keys are already gone).
//   - Does NOT touch vectors, other payload fields, or the SQLite database.
//
// Usage:
//
//	cleaner := qdrant.NewLocatorCleaner(client, schema, log)
//	report, err := cleaner.CleanLocators(ctx, false) // dry-run
//	report, err := cleaner.CleanLocators(ctx, true)  // apply
package qdrant

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// LocatorCleaner strips legacy drive_link and local_path payload keys
// from Qdrant points. It consumes the Client + IndexSchema to resolve
// the active collection via the runtime alias.
type LocatorCleaner struct {
	client *Client
	schema *IndexSchema
	log    *zap.Logger
}

// NewLocatorCleaner creates a LocatorCleaner.
func NewLocatorCleaner(client *Client, schema *IndexSchema, log *zap.Logger) *LocatorCleaner {
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
// The active collection is resolved from the IndexSchema's
// RuntimeAlias. Returns an error only when the alias cannot be
// resolved or the scroll/delete infrastructure fails entirely;
// per-point errors are captured in the report's Errors slice.
func (c *LocatorCleaner) CleanLocators(ctx context.Context, apply bool) (*LocatorCleanupReport, error) {
	collection, err := c.client.GetAliasTarget(ctx, c.schema.RuntimeAlias)
	if err != nil {
		return nil, fmt.Errorf("resolve alias target: %w", err)
	}
	if collection == "" {
		return nil, fmt.Errorf("runtime alias %q has no target — run EnsureSchema first", c.schema.RuntimeAlias)
	}

	report := &LocatorCleanupReport{
		DryRun:     !apply,
		Collection: collection,
	}

	// Scroll all points, accumulating those with legacy keys.
	var affectedIDs []string
	offset := ""
	for {
		page, err := c.client.ScrollPoints(ctx, collection, offset, scrollBatchSize)
		if err != nil {
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
				affectedIDs = append(affectedIDs, pt.ID)
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

	// In dry-run mode, report only — no mutations.
	if !apply {
		c.log.Info("locator-cleaner dry-run complete",
			zap.Int("total_scrolled", report.TotalPointsScrolled),
			zap.Int("affected", report.PointsAffected))
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
			// Continue with remaining batches — don't abort on partial failure.
			continue
		}
		report.BatchCount++
		report.KeysRemoved += len(batch) * len(legacyKeys)
	}

	c.log.Info("locator-cleaner apply complete",
		zap.Int("total_scrolled", report.TotalPointsScrolled),
		zap.Int("affected", report.PointsAffected),
		zap.Int("keys_removed", report.KeysRemoved),
		zap.Int("batch_count", report.BatchCount),
		zap.Int("error_count", len(report.Errors)))

	if len(report.Errors) > 0 {
		return report, fmt.Errorf("locator cleanup completed with %d batch errors", len(report.Errors))
	}
	return report, nil
}

// hasKey returns true if the payload map contains the given key
// (regardless of the value). A legacy key present but empty is
// still eligible for cleanup.
func hasKey(payload map[string]interface{}, key string) bool {
	if payload == nil {
		return false
	}
	_, ok := payload[key]
	return ok
}
