package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/media/vectorstore"
)

// ghostSweepable is the minimal Store subset the ghost sweeper needs.
// Inlined as a tiny interface (instead of importing vectorstore.Store
// with its 17 methods) so unit tests can mock just this much.
//
// Production callers pass *vectorstore.Service which satisfies it.
type ghostSweepable interface {
	ScrollAssetIDsPage(ctx context.Context, batchSize int, fn func([]string) error) error
	DeletePoints(ctx context.Context, assetIDs []string) error
}

// startQdrantGhostSweeper runs daily and removes "ghost" Qdrant points
// whose asset_id has NO matching row in the SQLite media_assets table.
//
// Why this matters: when the SQLite row is hard-deleted (manual purge,
// FK cascade, etc.) but the Qdrant point survives — usually because the
// outbox/cleanup path didn't reach that specific row — semantic search
// starts returning stale record_ids to handlers and Handlers.md §Indexer
// will cite ghost totals in realtime.Service.IndexHealth as
// orphan_in_qdrant. This sweeper closes the loop daily so the cross-check
// gap stays bounded by 24h instead of growing indefinitely.
//
// Flow:
//  1. Bulk-load every media_assets.id from SQLite into a hash set.
//  2. Stream ALL Qdrant asset_ids via ScrollAssetIDsPage.
//  3. Diff: ghosts = Qdrant IDs − SQLite IDs.
//  4. Delete ghosts via DeletePoints (filter on payload.asset_id).
//  5. Log total_deleted + tombstone sample for ops forensics.
//
// Idempotent: a partial run is fine — the next scheduled tick picks up
// where the previous one left off. Soft-deleted SQLite rows are NOT
// treated as missing (the live `id` is still present in the table;
// cleanup of those orphan points is the responsibility of the clip
// delete path, not this sweeper).
//
// Conservative on work-budget: a hard 30m ceiling per pass so a runaway
// Qdrant or stuck SELECT cannot starve other maintenance sweepers.
func startQdrantGhostSweeper(ctx context.Context, vectorSvc *vectorstore.Service, db *sql.DB, log *zap.Logger) {
	const (
		initialDelay    = 10 * time.Minute // out-of-phase with startQdrantCleaner (5m) and startClipDedupSweeper (2m)
		interval        = 24 * time.Hour   // daily per requirement
		scrollBatchSize = 500
		sqlitePageSize  = 1000
		maxWorkBudget   = 30 * time.Minute
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sweep := func() {
		sCtx, cancel := context.WithTimeout(ctx, maxWorkBudget)
		defer cancel()
		deleted, err := runGhostSweep(sCtx, vectorSvc, db, scrollBatchSize, sqlitePageSize, log)
		if err != nil {
			log.Warn("Qdrant ghost sweep failed", zap.Error(err))
			return
		}
		if deleted > 0 {
			log.Info("Qdrant ghost points removed", zap.Int("deleted", deleted))
		} else {
			log.Info("Qdrant ghost sweep clean (no drift detected)", zap.Int("deleted", 0))
		}
	}

	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

// runGhostSweep performs a single ghost-sweep pass. Exported (lower-case)
// so sweepers_test.go can call it directly without the daily ticker.
// Returned int is the number of Qdrant points actually deleted.
func runGhostSweep(ctx context.Context, qdrant ghostSweepable, db *sql.DB, scrollBatchSize, sqlitePageSize int, log *zap.Logger) (int, error) {
	if qdrant == nil {
		return 0, fmt.Errorf("qdrant store is nil")
	}
	if db == nil {
		return 0, fmt.Errorf("sqlite db is nil")
	}
	if scrollBatchSize <= 0 {
		scrollBatchSize = 500
	}
	if sqlitePageSize <= 0 {
		sqlitePageSize = 1000
	}

	// 1. Bulk-fetch every media_assets.id from SQLite, paginated to keep
	// memory bounded. ALL rows count — soft-deletes are still "present"
	// from Qdrant's perspective, so soft-delete ghosts are not our job.
	sqliteIDs := make(map[string]struct{}, 8192)
	offset := 0
	for {
		rows, err := db.QueryContext(ctx, `SELECT id FROM media_assets LIMIT ? OFFSET ?`, sqlitePageSize, offset)
		if err != nil {
			return 0, fmt.Errorf("query sqlite asset ids (limit=%d, offset=%d): %w", sqlitePageSize, offset, err)
		}
		batchN := 0
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, fmt.Errorf("scan asset id at offset %d: %w", offset, err)
			}
			sqliteIDs[id] = struct{}{}
			batchN++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("iterate sqlite asset ids: %w", err)
		}
		if batchN < sqlitePageSize {
			break
		}
		offset += sqlitePageSize
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
	}
	log.Debug("ghost sweep loaded sqlite ids", zap.Int("count", len(sqliteIDs)))

	// 2. Stream Qdrant and accumulate ghosts.
	var ghosts []string
	scrollErr := qdrant.ScrollAssetIDsPage(ctx, scrollBatchSize, func(batch []string) error {
		for _, id := range batch {
			if _, ok := sqliteIDs[id]; !ok {
				ghosts = append(ghosts, id)
			}
		}
		return nil
	})
	if scrollErr != nil {
		return 0, fmt.Errorf("scroll qdrant asset ids: %w", scrollErr)
	}

	log.Debug("ghost sweep scanned qdrant",
		zap.Int("sqlite", len(sqliteIDs)),
		zap.Int("ghosts", len(ghosts)))
	if len(ghosts) == 0 {
		return 0, nil
	}

	// 3. Delete ghosts. DeletePoints handles internal chunking at
	// ghostSweepDeleteBatch (100). For >=10k ghosts this becomes a
	// meaningful log+delete loop so we cap at 100/page to keep log noise
	// proportional to drift arrived in one tick.
	for i := 0; i < len(ghosts); i += 100 {
		end := i + 100
		if end > len(ghosts) {
			end = len(ghosts)
		}
		if err := qdrant.DeletePoints(ctx, ghosts[i:end]); err != nil {
			return i, fmt.Errorf("delete ghosts %d-%d: %w", i, end, err)
		}
	}

	// 4. Tombstone sample for ops forensics (debug level — operators
	// enable zap.Debug on the pipelinegen logger to see WHICH ghost_ids
	// were removed, critical for tracing the upstream cause).
	sample := ghosts
	if len(sample) > 20 {
		sample = sample[:20]
	}
	log.Debug("Qdrant ghost points deleted — sample",
		zap.Int("total_deleted", len(ghosts)),
		zap.Strings("sample_ids", sample))

	return len(ghosts), nil
}
