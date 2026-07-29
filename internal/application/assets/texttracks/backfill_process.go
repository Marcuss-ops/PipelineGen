// Package texttracks — backfill_process.go (LEAF):
//
// Per-clip single-asset pipeline. The CORE orchestrator for the
// asset scope: takes one asset, decides source-READY vs
// NEEDS-ACQUIRE vs OUT-OF-LUCK, then fans out via the
// Materializer.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// "process ONE clip for text-track backfill" decision. The CLI
// drives the loop via backfill_run.go; the per-clip decision
// MUST NOT be re-implemented inline by other leaves.
//
// Cross-file callers (same package):
//   - backfill.go      : declares BackfillService struct +
//     BackfillAssetResult DTO that this
//     method populates.
//   - backfill_run.go  : calls ProcessAsset inside the per-
//     asset loop.
//   - backfill_acquire.go : owns tryAcquire which this method
//     invokes when the source is missing
//     AND the acquirer is wired.
package texttracks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ProcessAsset runs the per-clip backfill pipeline:
//
//  1. Look up the source track in asset_text_tracks for the
//     (asset, sourceLanguage, textKind) triple.
//  2. If the source is not READY AND acquirer is wired → call
//     AcquireService.Acquire (priorities 2-5). On success,
//     save the acquired text as a READY source track and
//     re-query.
//  3. If the source is STILL not READY (acquisition failed or
//     acquirer is nil) → surface a typed error and skip
//     (no fan-out).
//  4. Call Materializer.Materialize which fan-outs to all
//     target languages, classifies (skip/create/retranslate),
//     upserts, and emits the asset.index.requested outbox event.
//
// Returns a typed result + a per-clip error. The error is
// nil on success (including when all target languages are
// skipped because they're already READY with the matching key).
func (s *BackfillService) ProcessAsset(
	ctx context.Context,
	assetItem *asset.Asset,
	opts BackfillOptions,
) (BackfillAssetResult, error) {
	start := time.Now()
	res := BackfillAssetResult{
		AssetID:        assetItem.ID,
		Source:         string(assetItem.Source),
		SourceLanguage: opts.SourceLanguage,
	}

	if assetItem == nil || assetItem.ID == "" {
		res.Err = "asset is nil or has empty id"
		res.DurationMs = time.Since(start).Milliseconds()
		return res, errors.New(res.Err)
	}

	// Step 1: read the source track. The Materializer does this
	// internally, but we need the source track's text_hash to
	// satisfy the Materialize signature. We piggyback on the
	// materializer's FindSourceTrack via a fresh Resolver
	// (constructed from the same ResolverConfig as the
	// materializer).
	//
	// godlike/06 SSOT: the per-(asset,kind) source lookup is
	// owned by Resolver.FindSourceTrack; the BackfillService
	// delegates to it instead of inlining the SQL.
	resolver := NewResolver(s.materializer.resolverCfg, s.materializer.repo, assetItem.ID, opts.TextKind)
	source, err := resolver.FindSourceTrack(ctx)
	if err != nil {
		var noSrc *ErrNoSourceTrack
		var notReady *ErrTrackNotReady
		switch {
		case errors.As(err, &noSrc):
			// Fase 5: try acquisition (priorities 2-5) before
			// giving up. If the acquirer is nil OR acquisition
			// fails, surface the original "no source" error.
			if s.acquirer != nil {
				acquired, acqErr := s.tryAcquire(ctx, assetItem, opts)
				if acqErr != nil {
					s.log.Warn("backfill: acquisition failed; clip will be skipped",
						zap.String("asset_id", assetItem.ID),
						zap.Error(acqErr))
					res.Err = "no_source_track"
					res.SourceReady = false
					res.DurationMs = time.Since(start).Milliseconds()
					return res, nil
				}
				res.SourceAcquired = true
				res.AcquiredFrom = acquiredFromLabel(acquired.SourceType)
				// Re-query the source track (now it should be READY).
				source, err = resolver.FindSourceTrack(ctx)
				if err != nil {
					res.Err = "acquired_but_save_failed"
					res.DurationMs = time.Since(start).Milliseconds()
					return res, nil
				}
				// Fall through to Materializer.
			} else {
				res.Err = "no_source_track"
				res.SourceReady = false
				res.DurationMs = time.Since(start).Milliseconds()
				s.log.Info("backfill: clip has no source track and AcquireService is not wired; run with AcquireService to fill the gap",
					zap.String("asset_id", assetItem.ID),
					zap.String("source_language", opts.SourceLanguage),
					zap.String("text_kind", string(opts.TextKind)))
				return res, nil
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
			return res, nil
		default:
			res.Err = err.Error()
			res.DurationMs = time.Since(start).Milliseconds()
			return res, fmt.Errorf("texttracks.BackfillService.ProcessAsset: find source: %w", err)
		}
	}

	// A legacy READY transcript can still be unusable for ASS when it has
	// no timed segments. Prefer a fresh timed acquisition from the repaired
	// local video before materialization; plain text alone is not subtitle
	// readiness.
	if err == nil && opts.TextKind == asset.TextTrackTranscript && s.acquirer != nil {
		_, timedCues, cueErr := s.repo.FindReady(ctx, assetItem.ID, opts.SourceLanguage, opts.TextKind)
		if cueErr == nil && len(timedCues) == 0 {
			acquired, acqErr := s.tryAcquire(ctx, assetItem, opts)
			if acqErr == nil {
				res.SourceAcquired = true
				res.AcquiredFrom = acquiredFromLabel(acquired.SourceType)
				source, err = resolver.FindSourceTrack(ctx)
			} else {
				s.log.Warn("backfill: READY transcript has no timed cues; acquisition failed",
					zap.String("asset_id", assetItem.ID), zap.Error(acqErr))
			}
		}
	}
	if err != nil {
		res.Err = "timed_source_track_unavailable"
		res.DurationMs = time.Since(start).Milliseconds()
		return res, nil
	}
	res.SourceReady = true

	// Step 4: call the Materializer with the source's text_hash.
	// The Materializer emits the asset.index.requested outbox
	// event when any language is created or retranslated, and
	// classifies (skip/retranslate) using the canonical
	// MaterializationKey (SourceVersion + ModelVersion) so
	// re-running is idempotent.
	report, err := s.materializer.Materialize(
		ctx,
		assetItem.ID,
		opts.SourceLanguage,
		source.TextHash,
		opts.TextKind,
		opts.TargetLanguages,
	)
	if err != nil {
		// A Materialize error is non-fatal at the per-clip
		// level: report carries the partial state. Surface
		// the error to the CLI so the report carries a
		// non-empty FailedLanguages map.
		res.DurationMs = time.Since(start).Milliseconds()
		res.Err = err.Error()
		if report != nil {
			res.CreatedLangs = append(res.CreatedLangs, report.CreatedLanguages...)
			res.SkippedLangs = append(res.SkippedLangs, report.SkippedLanguages...)
			res.Retranslated = append(res.Retranslated, report.RetranslatedLanguages...)
			for k := range report.FailedLanguages {
				res.FailedLangs = append(res.FailedLangs, k)
			}
		}
		return res, fmt.Errorf("texttracks.BackfillService.ProcessAsset: materialize: %w", err)
	}

	res.CreatedLangs = append(res.CreatedLangs, report.CreatedLanguages...)
	res.SkippedLangs = append(res.SkippedLangs, report.SkippedLanguages...)
	res.Retranslated = append(res.Retranslated, report.RetranslatedLanguages...)
	for k := range report.FailedLanguages {
		res.FailedLangs = append(res.FailedLangs, k)
	}

	// Step 5: generate ASS subtitle artifacts if this asset requires subtitles.
	if asset.RequiresSubtitles(string(assetItem.Source)) && opts.TextKind == asset.TextTrackTranscript {
		languages := append([]string{opts.SourceLanguage}, opts.TargetLanguages...)
		// Acquisition may resolve to a different language than the
		// requested one (for example, the first available YouTube/Whisper
		// track). Include every READY language so the clip still receives
		// its ASS artifact when that track has timed cues.
		readyLanguages, langErr := s.repo.ListReadyLanguages(ctx, assetItem.ID, opts.TextKind)
		if langErr != nil {
			s.log.Warn("backfill: list ready languages for ASS generation failed",
				zap.String("asset_id", assetItem.ID), zap.Error(langErr))
		} else {
			languages = append(languages, readyLanguages...)
		}
		uniqueLangs := make(map[string]bool)
		for _, l := range languages {
			if l != "" {
				uniqueLangs[l] = true
			}
		}

		clipContentHash := ""
		if assetItem.Metadata != nil {
			if ch, ok := assetItem.Metadata["content_hash"].(string); ok {
				clipContentHash = ch
			}
		}
		if clipContentHash == "" {
			clipContentHash = assetItem.FileHash()
		}
		if clipContentHash == "" {
			clipContentHash = assetItem.ID
		}

		driveFolderID := assetItem.FolderID()
		if driveFolderID == "" {
			driveFolderID = s.driveFolderID
		}
		for lang := range uniqueLangs {
			track, cues, err := s.repo.FindReady(ctx, assetItem.ID, lang, opts.TextKind)
			if err != nil {
				s.log.Warn("backfill: find ready track for ASS generation failed",
					zap.String("asset_id", assetItem.ID),
					zap.String("lang", lang),
					zap.Error(err))
				continue
			}
			if track == nil || len(cues) == 0 {
				continue
			}
			_, mErr := s.subMaterializer.Materialize(ctx, SubtitleMaterializerInput{
				AssetID:         assetItem.ID,
				LanguageCode:    lang,
				TextTrackID:     track.ID,
				ClipDurationMs:  assetItem.Duration.Milliseconds(),
				TimedCues:       cues,
				SubtitleStyleID: "vidrush-default",
				ClipContentHash: clipContentHash,
				DriveFolderID:   driveFolderID,
			})
			if mErr != nil {
				s.log.Warn("backfill: ASS materialization failed",
					zap.String("asset_id", assetItem.ID),
					zap.String("lang", lang),
					zap.Error(mErr))
			}
		}
	}

	res.DurationMs = time.Since(start).Milliseconds()
	return res, nil
}
