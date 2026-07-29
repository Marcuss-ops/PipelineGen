// Package app — background sweeper functions extracted from lifecycle.go.
//
// Per AGENTS.md Pattern 5 (June 2026): each file covers ONE concept.
// This file holds the ticker-driven background sweeper goroutines.
package app

import (
	"context"
	"fmt"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"go.uber.org/zap"
)

func startResearchCacheSweeper(ctx context.Context, repo *sqlitescripts.ScriptRepository, log *zap.Logger) {
	const (
		initialDelay = 30 * time.Second
		interval     = 6 * time.Hour
		maxAgeDays   = 30
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	sweep := func() {
		sCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		expired, err := repo.SweepExpiredResearchCache(sCtx)
		if err != nil {
			log.Warn("research_cache expired sweep failed", zap.Error(err))
			return
		}
		if expired > 0 {
			log.Info("research_cache expired swept", zap.Int64("expired_deleted", expired))
		}

		stale, err := repo.SweepStaleResearchCache(sCtx, maxAgeDays)
		if err != nil {
			log.Warn("research_cache stale sweep failed", zap.Error(err))
			return
		}
		if stale > 0 {
			log.Info("research_cache stale swept", zap.Int64("stale_deleted", stale), zap.Int("max_age_days", maxAgeDays))
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

// startQdrantHealthMonitor was removed during earlier cleanup. The
// capability deleted.

func startClipDedupSweeper(ctx context.Context, clipsRepo *assets.ClipsRepository, log *zap.Logger) {
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

func runDedupSweep(ctx context.Context, clipsRepo *assets.ClipsRepository, log *zap.Logger) (int, error) {
	rows, err := clipsRepo.DB().QueryContext(ctx, `
		SELECT json_extract(metadata_json, '$.youtube_video_id') AS vid, COUNT(*) AS n
		FROM media_assets
		WHERE `+asset.SoftDeleteFilter()+`
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

// startQdrantCleaner was removed during earlier cleanup; the
// deleted. Dead-link drift is now caught by the SQLite metadata layer
// (media_assets.drive_file_id_clean += json_extract checks) and the
// existing clip-dedup sweeper already enumerates sqliteIDs.

func startGemmaMemorySweeper(ctx context.Context, repo scriptports.MemoryGate, log *zap.Logger) {
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

func startVLMAutoTagSweeper(ctx context.Context, autotagSvc *autotag.Service, log *zap.Logger) {
	const (
		initialDelay = 1 * time.Minute
		interval     = 15 * time.Minute
		batchSize    = 10
		// claimFence is the canonical proof-window: rows whose
		// enrich_state_updated_at is more recent than
		// now()-claimFence are SKIPPED so a slow VLM call on row X
		// (claimed at T0) doesn't get re-claimed at T0+1min by an
		// overlapping sweep tick. Mirrors the PR-EMBEDDING-CHANNEL-
		// REGISTRY claim-fence pattern (godlike/06 SSOT: every first-
		// class sweep claim is gated by an updated_at fence).
		claimFence = 30 * time.Second
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
		// PR-ENRICHMENT-STATE-MACHINE (July 2026): the VLM sweeper
		// filter is the typed-state filter, NOT the legacy
		// "untagged" JSON-Extract query. The typed canonical path is:
		//   SELECT id FROM media_assets
		//   WHERE enrich_state = 'PENDING'
		//     AND enrich_state_updated_at < (now - 30s claim-fence)
		//     AND media_type != 'folder'
		//     AND local_path != ''
		//   ORDER BY enrich_state_updated_at ASC
		//   LIMIT 10
		// FAILED is terminal; an operator-reset row must first be
		// transitioned back to PENDING before it can be claimed. The
		// 30s claim fence prevents overlapping ticks from racing on a
		// row whose VLM call is in-flight.
		processed, err := autotagSvc.ProcessByEnrichCandidates(sCtx, batchSize, claimFence)
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

// ghostSweepable, startQdrantGhostSweeper, and
// runGhostSweep were removed — Qdrant capability deleted. Ghost-point
// cleanup is now exclusively a SQLite concern (handled by the
// clip-dedup sweeper plus future drift checks on
// media_assets.drive_file_id).
