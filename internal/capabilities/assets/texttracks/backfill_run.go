// Package texttracks — backfill_run.go (LEAF):
//
// High-level driver loop for the backfill pipeline. Aggregates
// the per-clip outcomes into a BackfillReport with the canonical
// counters (SourceReady / SourceAcquired / SourceMissing /
// SkippedOnlyMissing / Created / Retranslated / FailedLang /
// FailedAssetIDs / SkippedAssetIDs).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// top-level "iterate candidate clips" loop. The CLI
// (cmd/admin/text_tracks_backfill.go) calls Run on the
// --apply path; the per-clip decision lives in
// backfill_process.go and MUST NOT be re-inlined here.
//
// Counter policy (godlike/07 fail-closed):
//
//   - SourceMissingCount is incremented when perErr != nil AND
//     res.Err is one of {"no_source_track",
//     "source_track_not_ready", "acquired_but_save_failed"};
//     the corresponding asset.ID is appended to FailedAssetIDs.
//   - SourceReadyCount / SourceAcquiredCount are incremented
//     on the success branch (perErr == nil).
//
// Cross-file callers (same package):
//   - backfill.go           : declares BackfillService struct +
//     BackfillReport + BackfillOptions
//     types this file populates.
//   - backfill_process.go   : the per-clip ProcessAsset that
//     this loop dispatches to.
//   - backfill_candidates.go: the pre-filter helpers this loop
//     uses (ListCandidates for the
//     candidate list; IsAssetMissingForTargetSet
//     for --only-missing short-circuit).
//   - cmd/admin/text_tracks_backfill.go: calls Run on the
//     --apply path.
package assets

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Run is the high-level driver: query candidates, optionally
// pre-filter via --only-missing, then process each asset. The
// per-asset processing is fail-soft (a single failed clip is
// logged + counted, not raised); the returned report carries
// the failure IDs so the CLI can surface them.
//
// godlike/06 SSOT: the iteration + counter-aggregation policy
// is owned ONLY here. Per-asset decisions delegate to
// ProcessAsset (backfill_process.go); pre-filter decisions
// delegate to IsAssetMissingForTargetSet (backfill_candidates.go).
func (s *BackfillService) Run(
	ctx context.Context,
	repo asset.TextTrackRepository,
	opts BackfillOptions,
) (*BackfillReport, error) {
	start := time.Now()
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	candidates, err := s.ListCandidates(ctx, opts)
	if err != nil {
		return nil, err
	}

	report := &BackfillReport{
		Source:          opts.Source,
		SourceLanguage:  opts.SourceLanguage,
		TargetLanguages: append([]string{}, opts.TargetLanguages...),
		TextKind:        string(opts.TextKind),
		TotalCandidates: len(candidates),
		PerAsset:        make([]BackfillAssetResult, 0, len(candidates)),
		FailedAssetIDs:  []string{},
		SkippedAssetIDs: []string{},
	}

	for i, item := range candidates {
		select {
		case <-ctx.Done():
			report.DurationMs = time.Since(start).Milliseconds()
			return report, ctx.Err()
		default:
		}

		// --only-missing pre-filter: skip the clip entirely
		// when all target languages are READY.
		if opts.OnlyMissing && repo != nil {
			missing, err := s.IsAssetMissingForTargetSet(ctx, repo, item.ID, opts)
			if err != nil {
				s.log.Warn("backfill: IsAssetMissingForTargetSet failed; processing anyway",
					zap.String("asset_id", item.ID),
					zap.Error(err))
			} else if !missing {
				report.SkippedAssetIDs = append(report.SkippedAssetIDs, item.ID)
				report.SkippedOnlyMissing++
				report.PerAsset = append(report.PerAsset, BackfillAssetResult{
					AssetID:        item.ID,
					Source:         string(item.Source),
					SourceLanguage: opts.SourceLanguage,
					Skipped:        true,
					SkipReason:     "all_target_languages_ready",
				})
				continue
			}
		}

		report.Processed++
		res, perErr := s.ProcessAsset(ctx, item, opts)
		report.PerAsset = append(report.PerAsset, res)

		switch {
		case perErr != nil && res.Err == "no_source_track":
			report.SourceMissingCount++
			report.FailedAssetIDs = append(report.FailedAssetIDs, item.ID)
		case perErr != nil && res.Err == "source_track_not_ready":
			report.SourceMissingCount++
			report.FailedAssetIDs = append(report.FailedAssetIDs, item.ID)
		case perErr != nil && res.Err == "acquired_but_save_failed":
			report.SourceMissingCount++
			report.FailedAssetIDs = append(report.FailedAssetIDs, item.ID)
		case perErr != nil:
			report.FailedAssetIDs = append(report.FailedAssetIDs, item.ID)
		default:
			if res.SourceReady {
				report.SourceReadyCount++
			}
			if res.SourceAcquired {
				report.SourceAcquiredCount++
			}
		}

		report.CreatedTotal += len(res.CreatedLangs)
		report.SkippedLangTotal += len(res.SkippedLangs)
		report.RetranslatedTotal += len(res.Retranslated)
		report.FailedLangTotal += len(res.FailedLangs)

		if (i+1)%50 == 0 {
			s.log.Info("backfill progress",
				zap.Int("processed", report.Processed),
				zap.Int("source_ready", report.SourceReadyCount),
				zap.Int("source_acquired", report.SourceAcquiredCount),
				zap.Int("source_missing", report.SourceMissingCount),
				zap.Int("skipped_only_missing", report.SkippedOnlyMissing),
				zap.Int("created_total", report.CreatedTotal),
				zap.Int("retranslated_total", report.RetranslatedTotal),
				zap.Int("failed_lang_total", report.FailedLangTotal),
				zap.Int("remaining", len(candidates)-i-1))
		}
	}

	report.DurationMs = time.Since(start).Milliseconds()
	return report, nil
}
