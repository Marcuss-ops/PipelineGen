// Package app — background sweeper functions extracted from go.
//
// Per AGENTS.md Pattern 5 (June 2026): each file covers ONE concept.
// This file holds the ticker-driven background sweeper goroutines.
package wiring

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"

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

// mediaYouTubeIDDuplicateReader is the consumer-owned port for the clip-dedup
// sweeper's media_assets scan.
//
// The engine is NOT part of the interface, and that is intentional: the sweeper
// does not decide which database holds media_assets, the composition root does.
// The concrete that production passes (pgmedia.MediaDuplicateGroupReader) is
// resolved from the same canonical committer as the persistence.AssetSoftDeleter
// beside it, so the scan and the retirement can never name different engines.
//
// The retired form took *imagesregistry.ClipsRepository, which made the engine a
// property of the parameter's type — the operational SQLite store — and that is
// how a media read got wired to the mirror in the first place.
type mediaYouTubeIDDuplicateReader interface {
	// DuplicateYouTubeIDGroups returns up to limit youtube_video_id groups
	// holding more than one live media asset.
	DuplicateYouTubeIDGroups(ctx context.Context, limit int) ([]pgmedia.YouTubeIDDuplicateGroup, error)

	// DuplicateAssetIDsByYouTubeID returns the live ids sharing videoID,
	// newest first, excluding excludeID. The ORDER is load-bearing: the sweep
	// keeps index 0 and retires the rest.
	DuplicateAssetIDsByYouTubeID(ctx context.Context, videoID, excludeID string) ([]string, error)
}

// dedupGroupLimit is the per-tick safety valve on how many duplicate groups one
// sweep visits. It was 500 in the retired SQLite statement and is preserved: the
// limit bounds the tick, it is not a contract about which groups get visited.
const dedupGroupLimit = 500

func startClipDedupSweeper(ctx context.Context, reader mediaYouTubeIDDuplicateReader, retire persistence.AssetSoftDeleter, log *zap.Logger) {
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
		swept, err := runDedupSweep(sCtx, reader, retire, log)
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

// runDedupSweep retires the redundant copies of every live media asset that
// shares a youtube_video_id with another, keeping the newest of each group.
//
// MEDIA-SSOT (P2-9 read side, final entry). This function used to scan
// media_assets and soft-delete through *imagesregistry.ClipsRepository — the
// OPERATIONAL SQLite handle — while PostgreSQL owned the table. That was not a
// read-only debt: the scan enumerated duplicates on the mirror and the
// retirement landed on the mirror, so a duplicate the scan found stayed live on
// the SSOT and was counted again on the next tick, forever. Migrating only the
// read would have kept that loop and made it quieter, which is why the two
// halves moved together.
//
// Both halves now come from the composition root (see
// mediaDuplicateGroupReaderFromCommitter + persistence.CanonicalAssetSoftDeleter
// in buildMaintenanceSteps), so the sweep finds duplicates where the canonical
// writer commits them and retires them where the canonical writer owns them.
// There is no SQLite path and no direct SQL here: this file no longer mentions
// media_assets at all, and the archcheck media-reader gate is what keeps it
// that way.
//
// Fail-closed contract. A nil reader or a nil retirer means the media plane is
// closed; the sweep returns an error instead of degrading onto another engine.
// The caller's construction site already skips the step in that case, so this
// is the second line of defence — it is what stops a future caller from
// re-introducing a sweep that enumerates one database and mutates another.
//
// Error policy is preserved from the retired form: a per-group lookup failure
// logs and continues with the remaining groups (one bad group must not abort a
// whole tick), while a scan-level failure aborts the tick.
func runDedupSweep(ctx context.Context, reader mediaYouTubeIDDuplicateReader, retire persistence.AssetSoftDeleter, log *zap.Logger) (int, error) {
	if reader == nil {
		return 0, fmt.Errorf("dedup sweep: media duplicate reader is not wired (media SSOT closed) — refusing to enumerate media_assets on a second engine")
	}
	if retire == nil {
		return 0, fmt.Errorf("dedup sweep: canonical media soft-deleter is not wired (media SSOT closed) — refusing to retire media_assets on a second engine")
	}

	groups, err := reader.DuplicateYouTubeIDGroups(ctx, dedupGroupLimit)
	if err != nil {
		return 0, fmt.Errorf("dedup sweep query: %w", err)
	}

	swept := 0
	for _, g := range groups {
		// excludeID is empty because the sweep retires from the whole group,
		// not relative to a caller-held asset: index 0 is the newest row, which
		// is the one kept.
		dupIDs, err := reader.DuplicateAssetIDsByYouTubeID(ctx, g.YouTubeVideoID, "")
		if err != nil {
			log.Warn("Duplicate asset id lookup failed", zap.String("video_id", g.YouTubeVideoID), zap.Error(err))
			continue
		}
		for i := 1; i < len(dupIDs); i++ {
			if err := retire.SoftDeleteAsset(ctx, dupIDs[i]); err != nil {
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
