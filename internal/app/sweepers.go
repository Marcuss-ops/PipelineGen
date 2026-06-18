package app

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	clipsrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	scriptrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/service/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/metrics"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// startResearchCacheSweeper deletes research_cache rows whose last_used is
// older than 30 days. Runs every 6 hours; logs a warning on error. The
// short initial delay avoids contention during server bootstrap.
func startResearchCacheSweeper(ctx context.Context, repo *scriptrepo.ScriptRepository, log *zap.Logger) {
	const (
		initialDelay = 30 * time.Second
		interval     = 6 * time.Hour
		maxAgeDays   = 30
	)
	// Wait for initial delay, but exit immediately if context is cancelled.
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// Run once immediately after the initial delay, then on each tick.
	sweep := func() {
		sCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		deleted, err := repo.SweepStaleResearchCache(sCtx, maxAgeDays)
		if err != nil {
			log.Warn("research_cache sweep failed", zap.Error(err))
			return
		}
		if deleted > 0 {
			log.Info("research_cache swept", zap.Int64("deleted", deleted), zap.Int("max_age_days", maxAgeDays))
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

// startQdrantHealthMonitor polls Qdrant's health endpoint on a tight
// interval (default 60s) and updates the QdrantHealthStatus Prometheus
// gauge, logging a warning the first time the status flips from
// healthy → unhealthy. The first run fires after a short initial delay
// so the server has time to finish bootstrapping.
func startQdrantHealthMonitor(ctx context.Context, vectorSvc *vectorstore.Service, log *zap.Logger) {
	const (
		initialDelay = 15 * time.Second
		interval     = 60 * time.Second
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var wasHealthy *bool
	check := func() {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		err := vectorSvc.Health(checkCtx)
		healthy := err == nil
		if wasHealthy == nil || *wasHealthy != healthy {
			// State change — log so operators notice.
			if healthy {
				log.Info("Qdrant is healthy", zap.String("check_interval", interval.String()))
			} else {
				log.Warn("Qdrant health check FAILED — semantic search/upserts may degrade",
					zap.Error(err))
			}
			wasHealthy = &healthy
		}
	}

	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

// startClipDedupSweeper scans the clips repo for groups of clips that
// share the same youtube_video_id (and matching start/end if present).
// For each group with >1 entries, the most-recently-created clip is kept
// and the rest are soft-deleted. Runs every 30 minutes by default.
func startClipDedupSweeper(ctx context.Context, clipsRepo *clipsrepo.Repository, log *zap.Logger) {
	const (
		initialDelay = 2 * time.Minute
		interval     = 30 * time.Minute
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sweep := func() {
		sCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		swept, err := runDedupSweep(sCtx, clipsRepo, log)
		if err != nil {
			log.Warn("clip dedup sweep failed", zap.Error(err))
			return
		}
		if swept > 0 {
			log.Info("clip dedup sweep completed", zap.Int("soft_deleted", swept))
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

// runDedupSweep finds clip groups that share a youtube_video_id and
// soft-deletes all but the newest entry. Returns the number of clips
// soft-deleted. Safe to call concurrently — it uses soft-delete
// (metadata_json.deleted_at), not HARD DELETE.
func runDedupSweep(ctx context.Context, clipsRepo *clipsrepo.Repository, log *zap.Logger) (int, error) {
	// Pull the list of distinct youtube_video_ids with duplicates.
	rows, err := clipsRepo.DB().QueryContext(ctx, `
		SELECT json_extract(metadata_json, '$.youtube_video_id') AS vid, COUNT(*) AS n
		FROM media_assets
		WHERE `+clipsRepo.SoftDeleteFilter()+`
		  AND json_extract(COALESCE(metadata_json,'{}'), '$.youtube_video_id') IS NOT NULL
		  AND json_extract(COALESCE(metadata_json,'{}'), '$.youtube_video_id') != ''
		GROUP BY vid
		HAVING n > 1
		LIMIT 500`)
	if err != nil {
		return 0, fmt.Errorf("dedup sweep query: %w", err)
	}
	defer rows.Close()

	type groupRow struct {
		vid string
		n   int
	}
	var groups []groupRow
	for rows.Next() {
		var g groupRow
		if err := rows.Scan(&g.vid, &g.n); err != nil {
			log.Warn("dedup sweep scan failed", zap.Error(err))
			continue
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("dedup sweep rows: %w", err)
	}

	swept := 0
	for _, g := range groups {
		dupIDs, err := clipsRepo.FindDuplicatesByYouTubeID(ctx, g.vid, "")
		if err != nil {
			log.Warn("FindDuplicatesByYouTubeID failed", zap.String("video_id", g.vid), zap.Error(err))
			continue
		}
		// dupIDs comes back ordered by created_at DESC, so [0] is the
		// newest. Soft-delete the rest.
		for i := 1; i < len(dupIDs); i++ {
			if err := clipsRepo.DeleteClip(ctx, dupIDs[i]); err != nil {
				log.Warn("dedup sweep soft-delete failed",
					zap.String("clip_id", dupIDs[i]), zap.Error(err))
				continue
			}
			swept++
			metrics.DedupMerged.WithLabelValues("youtube-manual", "sweeper").Inc()
		}
	}
	return swept, nil
}

// startQdrantCleaner periodically validates Drive links in Qdrant points.
// Points whose Drive files have been trashed/deleted are removed so that
// semantic search never returns dead links. Runs every 12 hours.
func startQdrantCleaner(ctx context.Context, vectorSvc *vectorstore.Service, driveUploader *drive.Uploader, log *zap.Logger) {
	const (
		initialDelay = 5 * time.Minute
		interval     = 12 * time.Hour
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	clean := func() {
		sCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()

		validator := func(assetID, driveFileID, driveLink string) (bool, error) {
			// Prefer drive_file_id over URL-based link for validation
			fileID := driveFileID
			if fileID == "" {
				var err error
				fileID, err = urlutil.FileIDFromDriveLink(driveLink)
				if err != nil || fileID == "" {
					return false, fmt.Errorf("cannot extract file ID from link %q: %w", driveLink, err)
				}
			}
			return driveUploader.FileIsNotTrashed(sCtx, fileID)
		}

		deleted, err := vectorSvc.CleanupStalePoints(sCtx, validator)
		if err != nil {
			log.Warn("Qdrant stale point cleanup failed", zap.Error(err))
			return
		}
		if deleted > 0 {
			log.Info("Qdrant stale points removed", zap.Int("deleted", deleted))
		}
	}

	clean()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			clean()
		}
	}
}

// startGemmaMemorySweeper periodically prunes gemma memory tables using
// SweepAll which combines decay, TTL deletion, per-channel capping, and
// chunk cleanup into a single transactional sweep.
func startGemmaMemorySweeper(ctx context.Context, repo *gemmamemory.Repository, log *zap.Logger) {
	const (
		initialDelay = 60 * time.Second
		interval     = 6 * time.Hour
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	sweep := func() {
		sCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		deleted, err := repo.SweepAll(sCtx)
		if err != nil {
			log.Warn("gemma memory sweep failed", zap.Error(err))
			return
		}
		if deleted > 0 {
			log.Info("gemma memory sweep completed", zap.Int64("total_deleted", deleted))
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

// startVLMAutoTagSweeper scans the database for untagged assets and triggers
// the VLM autotag service. Runs every 15 minutes by default.
func startVLMAutoTagSweeper(ctx context.Context, autotagSvc *autotag.Service, log *zap.Logger) {
	const (
		initialDelay = 1 * time.Minute
		interval     = 15 * time.Minute
		batchSize    = 10
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	process := func() {
		sCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		processed, err := autotagSvc.ProcessUntagged(sCtx, batchSize)
		if err != nil {
			log.Warn("vlm auto-tag sweep failed", zap.Error(err))
			return
		}
		if processed > 0 {
			log.Info("vlm auto-tag sweep completed", zap.Int("processed", processed))
		}
	}

	process()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			process()
		}
	}
}
