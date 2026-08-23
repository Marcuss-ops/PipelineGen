// Package usecase — process_segment_step2.go: canonical owner of
// Step 2 (clip cache lookup + StrategyReplace bypass).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SOLE owner of the `Cache.GetExisting` lookup inside the Execute
// pipeline. StrategyReplace bypass is a Commit 2/6 #2 invariant —
// when `cmd.Strategy == StrategyReplace`, the cache lookup is
// SKIPPED entirely so a re-extract under the same clipID always
// re-runs the full 9-step pipeline (no false cache hits on
// explicitly-forced re-extract).
//
// godlike/07 no-fake-availability: a cache-lookup FAILURE (cacheErr)
// is Warn-logged and the pipeline FALLS THROUGH to re-process. The
// pre-Commit-2 behavior swallowed the error silently which masked
// infrastructure outages. Warn-level observability is the canonical
// surface for transient cache misses (godlike/07 minimum-blast-radius:
// the cache may legitimately be empty on a fresh deploy).
package usecase

import (
	"context"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// step2_CacheLookup is the canonical owner of Step 2. Returns:
//
//   - cacheHit=true: the orchestrator must SKIP acquisition/cut
//     (Steps 3-5) + ffprobe (Step 5a) and continue to the
//     enrichment/finalization gate (Steps 6-9). This method surfaces
//     the cached binary coordinates on `out` (Item.LocalPath +
//     Item.LegacyFileMD5 + Item.DriveFileID + Item.DriveLink +
//     Item.DownloadLink) so Execute can finalize without re-acquiring
//     the binary. The status is NOT set here — Execute stamps
//     "processed" after the finalization gate runs.
//   - cacheHit=false + err=nil: pipeline should proceed to step 3+
//   - cacheHit=false + err=typed: pipeline should propagate the error
//     (the orchestrator returns out, err immediately)
//
// godlike/06 SSOT: StrategyReplace short-circuit semantics preserved
// EXACTLY (no log spam on the forced-re-extract path). Warn-log on
// cacheErr is preserved EXACTLY.
//
// PR-CACHE-HIT-FINALIZATION: the "skipped" early-return on cache hit
// is RETIRED. A cache hit no longer short-circuits the whole pipeline
// — it only skips binary acquisition, so a cached clip whose metadata
// or text tracks are missing/stale still passes through the canonical
// enrichment gate and gets repaired.
func (u *ProcessYouTubeSegmentUseCase) step2_CacheLookup(
	ctx context.Context,
	cmd youtubetypes.ProcessSegmentCommand,
	out *youtubetypes.ProcessSegmentResult,
	clipID string,
) (bool, error) {
	// StrategyReplace bypasses the cache lookup entirely so a
	// re-extract under the same clipID always re-runs the full
	// pipeline.
	if u.core.Cache != nil && cmd.Strategy != youtubetypes.StrategyReplace {
		existingItem, exists, cacheErr := u.core.Cache.GetExisting(ctx, clipID)
		// A cache hit is authoritative only when the persisted artifact is
		// complete enough to satisfy the canonical SQLite/Drive gate. Older
		// rows may have a Drive ID but missing duration/hash; those must be
		// reprocessed so a green cache response cannot preserve partial state.
		cacheComplete := existingItem != nil && existingItem.Duration > 0 &&
			existingItem.LegacyFileMD5 != "" && existingItem.DriveFileID != ""
		if cacheErr == nil && exists && cacheComplete {
			out.Item.LocalPath = existingItem.LocalPath
			out.Item.LegacyFileMD5 = existingItem.LegacyFileMD5
			out.Item.DriveFileID = existingItem.DriveFileID
			out.Item.DriveLink = existingItem.DriveLink
			out.Item.DownloadLink = existingItem.DownloadLink
			return true, nil
		}
		if cacheErr != nil {
			u.core.Log.Warn("clip cache lookup failed; falling through to re-process",
				zap.String("clip_id", clipID), zap.Error(cacheErr))
		}
	}
	return false, nil
}
