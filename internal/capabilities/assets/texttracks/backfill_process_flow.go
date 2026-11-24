package texttracks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ProcessAsset runs the per-clip backfill pipeline: source lookup and optional
// acquisition, timed-cue repair, materialization, then subtitle delivery.
// Per-clip failures are reported in the result; the error is non-nil only for
// caller-level failures.
func (s *BackfillService) ProcessAsset(
	ctx context.Context,
	assetItem *asset.Asset,
	opts BackfillOptions,
) (BackfillAssetResult, error) {
	start := time.Now()
	res := BackfillAssetResult{SourceLanguage: opts.SourceLanguage}
	if assetItem != nil {
		res.AssetID = assetItem.ID
		res.Source = string(assetItem.Source)
	}
	if assetItem == nil || assetItem.ID == "" {
		res.Err = "asset is nil or has empty id"
		res.DurationMs = time.Since(start).Milliseconds()
		return res, errors.New(res.Err)
	}

	resolverCfg := s.materializer.resolverCfg
	resolverCfg.SourceLanguage = opts.SourceLanguage
	resolver := NewResolver(resolverCfg, s.materializer.repo, assetItem.ID, opts.TextKind)

	source, done, err := s.lookupBackfillSource(ctx, assetItem, opts, resolver, &res, start)
	if done {
		return res, err
	}
	res.SourceReady = true

	report, err := s.materializer.Materialize(
		ctx,
		assetItem.ID,
		opts.SourceLanguage,
		source.TextHash,
		opts.TextKind,
		opts.TargetLanguages,
	)
	if err != nil {
		res.DurationMs = time.Since(start).Milliseconds()
		res.Err = err.Error()
		recordBackfillReport(&res, report)
		return res, fmt.Errorf("texttracks.BackfillService.ProcessAsset: materialize: %w", err)
	}
	recordBackfillReport(&res, report)
	s.deliverBackfillSubtitles(ctx, assetItem, opts, &res)

	res.DurationMs = time.Since(start).Milliseconds()
	return res, nil
}

func (s *BackfillService) lookupBackfillSource(
	ctx context.Context,
	assetItem *asset.Asset,
	opts BackfillOptions,
	resolver *Resolver,
	res *BackfillAssetResult,
	start time.Time,
) (*detail.TextTrack, bool, error) {
	source, err := resolver.FindSourceTrack(ctx)
	if err != nil {
		var noSrc *ErrNoSourceTrack
		var notReady *ErrTrackNotReady
		switch {
		case errors.As(err, &noSrc):
			source, done, acqErr := s.acquireMissingSource(ctx, assetItem, opts, resolver, res, start)
			if done || acqErr != nil {
				return source, done, acqErr
			}
		case errors.As(err, &notReady):
			res.Err = "source_track_not_ready"
			res.SourceReady = false
			res.DurationMs = time.Since(start).Milliseconds()
			s.log.Info("backfill: source track is not READY",
				zap.String("asset_id", assetItem.ID),
				zap.String("source_language", opts.SourceLanguage),
				zap.String("current_status", string(notReady.CurrentStatus)),
				zap.Strings("ready_languages", notReady.AvailableLanguages))
			return nil, true, nil
		default:
			res.Err = err.Error()
			res.DurationMs = time.Since(start).Milliseconds()
			return nil, false, fmt.Errorf("texttracks.BackfillService.ProcessAsset: find source: %w", err)
		}
	}
	return s.repairUntimedSource(ctx, assetItem, opts, resolver, source, res, start)
}

func (s *BackfillService) acquireMissingSource(
	ctx context.Context,
	assetItem *asset.Asset,
	opts BackfillOptions,
	resolver *Resolver,
	res *BackfillAssetResult,
	start time.Time,
) (*detail.TextTrack, bool, error) {
	if s.acquirer == nil {
		res.Err = "no_source_track"
		res.SourceReady = false
		res.DurationMs = time.Since(start).Milliseconds()
		s.log.Info("backfill: clip has no source track and AcquireService is not wired; run with AcquireService to fill the gap",
			zap.String("asset_id", assetItem.ID),
			zap.String("source_language", opts.SourceLanguage),
			zap.String("text_kind", string(opts.TextKind)))
		return nil, true, nil
	}

	acquired, acqErr := s.tryAcquire(ctx, assetItem, opts)
	if acqErr != nil {
		s.log.Warn("backfill: acquisition failed; clip will be skipped",
			zap.String("asset_id", assetItem.ID),
			zap.Error(acqErr))
		res.Err = "no_source_track"
		res.SourceReady = false
		res.DurationMs = time.Since(start).Milliseconds()
		return nil, true, nil
	}
	res.SourceAcquired = true
	res.AcquiredFrom = acquiredFromLabel(acquired.SourceType)

	source, err := resolver.FindSourceTrack(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			res.Err = "acquired_but_reread_cancelled"
			res.SourceReady = false
			res.DurationMs = time.Since(start).Milliseconds()
			s.log.Warn("backfill: re-read after acquisition was cancelled; the saved track will be picked up on the retry",
				zap.String("asset_id", assetItem.ID),
				zap.Error(ctxErr))
			return nil, true, fmt.Errorf("backfill: re-read acquired source track for asset %s: %w", assetItem.ID, ctxErr)
		}
		res.Err = "acquired_but_save_failed"
		res.DurationMs = time.Since(start).Milliseconds()
		return nil, true, nil
	}
	return source, false, nil
}

func (s *BackfillService) repairUntimedSource(
	ctx context.Context,
	assetItem *asset.Asset,
	opts BackfillOptions,
	resolver *Resolver,
	source *detail.TextTrack,
	res *BackfillAssetResult,
	start time.Time,
) (*detail.TextTrack, bool, error) {
	var rereadErr error
	if opts.TextKind == detail.TextTrackTranscript && s.acquirer != nil {
		_, timedCues, cueErr := s.repo.FindReady(ctx, assetItem.ID, opts.SourceLanguage, opts.TextKind)
		if cueErr == nil && len(timedCues) == 0 {
			acquired, acqErr := s.tryAcquire(ctx, assetItem, opts)
			if acqErr == nil {
				res.SourceAcquired = true
				res.AcquiredFrom = acquiredFromLabel(acquired.SourceType)
				source, rereadErr = resolver.FindSourceTrack(ctx)
			} else {
				s.log.Warn("backfill: READY transcript has no timed cues; acquisition failed",
					zap.String("asset_id", assetItem.ID), zap.Error(acqErr))
			}
		}
	}
	if rereadErr != nil {
		res.Err = "timed_source_track_unavailable"
		res.DurationMs = time.Since(start).Milliseconds()
		return nil, true, nil
	}
	return source, false, nil
}

func recordBackfillReport(res *BackfillAssetResult, report *MaterializationReport) {
	if report == nil {
		return
	}
	res.CreatedLangs = append(res.CreatedLangs, report.CreatedLanguages...)
	res.SkippedLangs = append(res.SkippedLangs, report.SkippedLanguages...)
	res.Retranslated = append(res.Retranslated, report.RetranslatedLanguages...)
	for lang := range report.FailedLanguages {
		res.FailedLangs = append(res.FailedLangs, lang)
	}
	res.IndexRepaired = report.IndexInputRebuilt
	res.ReindexRequested = report.ReindexRequested
}

func (s *BackfillService) deliverBackfillSubtitles(
	ctx context.Context,
	assetItem *asset.Asset,
	opts BackfillOptions,
	res *BackfillAssetResult,
) {
	if opts.SkipSubtitleMaterialization {
		return
	}
	delivery, err := s.MaterializeSubtitleArtifacts(ctx, assetItem, opts.SourceLanguage, opts.TargetLanguages, opts.TextKind)
	if err != nil {
		s.log.Warn("backfill: subtitle artifact delivery failed",
			zap.String("asset_id", assetItem.ID), zap.Error(err))
	}
	res.SubtitlesDelivered = delivery.Delivered
}
