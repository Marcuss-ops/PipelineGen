package indexing

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// UpsertFromClips reads multiple clips and batch-upserts them.
// Implements clipindexer.VectorStoreIndexer.
//
// HIGH #4 (July 2026): partial failures now return a typed *transport.PartialUpsertError
// with per-asset phase (fetch/map/upsert), cause, and retryability. The
// previous implementation lost the original error and reduced failures to
// a count-only summary.
func (w *IndexWriter) UpsertFromClips(ctx context.Context, clipIDs []string) error {
	if len(clipIDs) == 0 {
		return nil
	}

	points := make([]schema.Point, 0, len(clipIDs))
	var failures []transport.AssetUpsertFailure

	for _, clipID := range clipIDs {
		asset, err := w.mapper.FetchAsset(ctx, clipID)
		if err != nil {
			w.log.Warn("failed to fetch asset for qdrant upsert",
				zap.String("asset_id", clipID),
				zap.Error(err))
			failures = append(failures, transport.AssetUpsertFailure{
				AssetID: clipID,
				Phase:   "fetch",
				Cause:   err,
			})
			continue
		}
		point, err := w.mapper.AssetToPoint(ctx, asset, w.idxSchema)
		if err != nil {
			w.log.Warn("failed to map asset to qdrant point",
				zap.String("asset_id", clipID),
				zap.Error(err))
			failures = append(failures, transport.AssetUpsertFailure{
				AssetID: clipID,
				Phase:   "map",
				Cause:   err,
			})
			continue
		}
		points = append(points, *point)
	}

	if len(points) == 0 {
		if len(failures) > 0 {
			// Build the typed error so callers can inspect per-failure
			// causes instead of parsing a flat count-only string.
			return newPartialUpsertError(nil, failures)
		}
		return nil
	}

	// PR 5 (June 2026, fix/qdrant-tenant-scope): write through the
	// runtime alias directly. Qdrant accepts an alias as the
	// collection name in PUT/POST /points requests and the
	// resulting write is atomic — no mid-flight alias-switch race,
	// no extra round-trip. Pre-PR5 the writer called
	// GetAliasTarget per upsert/delete batch, paying one HTTP call
	// AND opening a window where a blue-green switch could land the
	// batch in the wrong physical collection.
	//
	// GetAliasTarget's legitimate uses (admin reconcile, DR, ensure
	// schema, snapshot) are unaffected — only the writer hot path
	// is changed.
	if err := w.projection.UpsertProjection(ctx, w.idxSchema.RuntimeAlias, points); err != nil {
		return fmt.Errorf("upsert %d points to %q: %w", len(points), w.idxSchema.RuntimeAlias, err)
	}

	w.log.Info("upserted points to qdrant",
		zap.Int("count", len(points)),
		zap.String("collection", w.idxSchema.RuntimeAlias))

	if len(failures) > 0 {
		successIDs := make([]string, 0, len(points))
		for _, p := range points {
			// Extract the canonical media_assets.id from the
			// payload (PayloadMapper always sets asset_id).
			// Fall back to the Qdrant point ID only when the
			// payload is missing — the canonical path never
			// hits this branch.
			assetID := p.ID
			if p.Payload != nil {
				if id, ok := p.Payload["asset_id"].(string); ok && id != "" {
					assetID = id
				}
			}
			successIDs = append(successIDs, assetID)
		}
		return newPartialUpsertError(successIDs, failures)
	}
	return nil
}

// DeletePoints deletes points from the active collection by asset ID.
// Implements outbox.VectorPointDeleter (PR 4).
//
// QDRANT-001 closure (June 2026): each asset ID is canonicalised via
// schema.AssetIDToQdrantPointID before being sent to the Qdrant client. The
// `transport.Client.DeletePoints` is intentionally linear (it does NOT translate
// the IDs) — that contract is split here so the transport.Client layer stays
// Qdrant-native and free of asset-domain knowledge. Mirrors the
// PayloadMapper.AssetToPoint write path so any asset ID passed in
// produces a 1-to-1 delete against the prefix-namespaced schema.Point ID
// that the mapper originally wrote.
func (w *IndexWriter) DeleteAssetPoints(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	// Canonicalise ids → Qdrant point IDs. Empty inputs become empty
	// strings; we pass them through unchanged so the Qdrant API
	// (which treats an empty point-id as a no-op) keeps its current
	// semantics in legacy callers that haven't yet trimmed.
	pointIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if pid := schema.AssetIDToQdrantPointID(id); pid != "" {
			pointIDs = append(pointIDs, pid)
		}
	}
	if len(pointIDs) == 0 {
		return nil
	}

	// PR 5 (June 2026, fix/qdrant-tenant-scope): same write-through-
	// alias rationale as UpsertFromClips above. The previous
	// alias-resolution round-trip is dropped; the alias name is
	// used directly as the Qdrant collection name in the delete
	// payload.
	if err := w.projection.DeleteProjection(ctx, w.idxSchema.RuntimeAlias, pointIDs); err != nil {
		return fmt.Errorf("delete points from %q: %w", w.idxSchema.RuntimeAlias, err)
	}

	w.log.Info("deleted points from qdrant",
		zap.Int("count", len(pointIDs)),
		zap.String("collection", w.idxSchema.RuntimeAlias))
	return nil
}

// ReindexAll reads all assets from the mapper's store via paginated
// cursor scan and upserts them into the given target collection.
//
// HIGH #8 (July 2026): replaced total-ID-in-memory + N+1 FetchAsset
// with cursor-based paginated scanning (WHERE id > ? ORDER BY id
// LIMIT 500). Each batch is fetched in a single SQL query; the
// writer receives complete batches without re-reading.
func (w *IndexWriter) ReindexAll(ctx context.Context, targetCollection string, limit int) (*schema.ReindexResult, error) {
	if targetCollection == "" {
		targetCollection = w.idxSchema.CanonicalName()
	}

	const pageSize = 500

	result := &schema.ReindexResult{
		TargetCollection: targetCollection,
	}

	var afterID string // cursor: tracks the last asset ID from the previous page
	var totalSeen int

	points := make([]schema.Point, 0, 100)
	var batchCount int

	for {
		// Respect the limit: if we've seen enough, stop.
		if limit > 0 && totalSeen >= limit {
			break
		}

		// Fetch one page of full AssetData rows.
		pageLimit := pageSize
		if limit > 0 && totalSeen+pageLimit > limit {
			pageLimit = limit - totalSeen
		}

		batch, err := w.mapper.FetchAssetBatch(ctx, afterID, pageLimit)
		if err != nil {
			return result, fmt.Errorf("reindex: fetch batch (after %q): %w", afterID, err)
		}
		if len(batch) == 0 {
			break // no more assets
		}

		result.TotalAssets += len(batch)
		totalSeen += len(batch)

		// Map each asset to a point and accumulate.
		for _, asset := range batch {
			afterID = asset.ID // advance cursor

			point, err := w.mapper.AssetToPoint(ctx, asset, w.idxSchema)
			if err != nil {
				result.FailedAssets++
				result.FailedAssetIDs = append(result.FailedAssetIDs, asset.ID)
				continue
			}
			points = append(points, *point)
			batchCount++

			// Flush every 100 points.
			if len(points) >= 100 {
				if err := w.projection.UpsertProjection(ctx, targetCollection, points); err != nil {
					return result, fmt.Errorf("reindex batch upsert: %w", err)
				}
				// Only count as indexed AFTER the batch commit
				// succeeds (Blocco 4c, July 2026).
				result.IndexedAssets += batchCount
				batchCount = 0
				points = points[:0]
			}
		}
	}

	// Flush remaining.
	if len(points) > 0 {
		if err := w.projection.UpsertProjection(ctx, targetCollection, points); err != nil {
			return result, fmt.Errorf("reindex final batch upsert: %w", err)
		}
		result.IndexedAssets += batchCount
	}

	w.log.Info("reindex complete",
		zap.Int("total", result.TotalAssets),
		zap.Int("indexed", result.IndexedAssets),
		zap.Int("failed", result.FailedAssets),
		zap.String("collection", targetCollection))

	return result, nil
}
