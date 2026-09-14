// Package texttracks — backfill_subtitles.go: the canonical per-language
// subtitle-artifact delivery step.
//
// POSTGRES-MEDIA-CUTOVER, September 2026. This step used to live inline in
// ProcessAsset. It is extracted here because it has TWO callers that must
// produce byte-identical artifacts:
//
//  1. the operator backfill (ProcessAsset), which walks the catalog, and
//  2. the `asset.text.materialize` job handler's fast path, which the direct
//     YouTube pipeline now fans out to after every successful clip commit.
//
// Before this extraction only (1) existed. A YouTube clip committed WITH a
// transcript took the fast path — the fan-out carries the committed source
// hash, so the handler never re-enters ProcessAsset — with the result that
// the clip got its 10 language rows but never got the 10 subtitle files
// uploaded to Drive. Two copies of this loop would have drifted (the style
// id, the content-hash fallback and the READY-language set are all
// correctness-relevant), so the loop has exactly one owner.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of
// "READY text tracks → .ass artifacts → Drive + subtitle_artifacts registry".
// Neither caller may re-implement any part of it.
package texttracks

import (
	"context"

	"go.uber.org/zap"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// SubtitleArtifactStyleID is the canonical ASS style for generated subtitle
// artifacts. Named here so the direct YouTube path and the operator backfill
// cannot disagree about it.
const SubtitleArtifactStyleID = "vidrush-default"

// SubtitleDeliveryReport is the per-clip result of the subtitle-artifact
// delivery step. A failure is recorded per language rather than returned as
// a fatal error: the text tracks are already durable at this point, so an
// upload problem must not fail the whole materialization. The caller decides
// whether to surface it.
type SubtitleDeliveryReport struct {
	// Delivered counts the languages whose artifact reached the registry
	// (and Drive, when a publisher is wired).
	Delivered int `json:"delivered"`
	// Languages lists the languages considered for delivery.
	Languages []string `json:"languages,omitempty"`
	// Failed maps language → error, for the artifacts that did not make it.
	Failed map[string]string `json:"failed,omitempty"`
	// Skipped reports that no artifact was attempted at all.
	Skipped bool `json:"skipped"`
	// SkipReason names why, so "not applicable" is never confused with
	// "silently did nothing".
	SkipReason string `json:"skip_reason,omitempty"`
}

// MaterializeSubtitleArtifacts generates and delivers one .ass subtitle
// artifact per READY language for a single asset. It is idempotent: the
// artifact materializer resolves a current READY artifact with matching
// text/cues/style/duration and reuses its Drive reference instead of
// uploading a duplicate, so calling this on every fan-out run costs a
// registry lookup rather than a re-upload.
//
// It is a no-op (not an error) when the source does not take subtitles, when
// the text kind is not a transcript, or when the asset ID is empty; the
// returned report says so.
func (s *BackfillService) MaterializeSubtitleArtifacts(
	ctx context.Context,
	assetItem *asset.Asset,
	sourceLanguage string,
	targetLanguages []string,
	kind detail.TextTrackKind,
) (SubtitleDeliveryReport, error) {
	rep := SubtitleDeliveryReport{Failed: map[string]string{}}

	if s == nil || assetItem == nil || assetItem.ID == "" {
		rep.Skipped = true
		rep.SkipReason = "no_asset"
		return rep, nil
	}
	if s.subMaterializer == nil {
		rep.Skipped = true
		rep.SkipReason = "no_subtitle_materializer"
		return rep, nil
	}
	if !detail.RequiresSubtitles(string(assetItem.Source)) {
		rep.Skipped = true
		rep.SkipReason = "source_does_not_take_subtitles"
		return rep, nil
	}
	if kind != detail.TextTrackTranscript {
		rep.Skipped = true
		rep.SkipReason = "text_kind_is_not_transcript"
		return rep, nil
	}

	languages := append([]string{sourceLanguage}, targetLanguages...)
	// Acquisition may resolve to a different language than the requested
	// one (for example, the first available YouTube/Whisper track).
	// Include every READY language so the clip still receives its
	// artifact when that track has timed cues.
	readyLanguages, langErr := s.repo.ListReadyLanguages(ctx, assetItem.ID, kind)
	if langErr != nil {
		s.log.Warn("texttracks.subtitles: list ready languages failed",
			zap.String("asset_id", assetItem.ID), zap.Error(langErr))
	} else {
		languages = append(languages, readyLanguages...)
	}

	uniqueLangs := make(map[string]bool)
	ordered := make([]string, 0, len(languages))
	for _, l := range languages {
		if l == "" || uniqueLangs[l] {
			continue
		}
		uniqueLangs[l] = true
		ordered = append(ordered, l)
	}
	rep.Languages = ordered

	clipContentHash := assetContentHash(assetItem)
	if clipContentHash == "" {
		clipContentHash = assetItem.ID
	}

	driveFolderID := assetItem.FolderID()
	if driveFolderID == "" {
		driveFolderID = s.driveFolderID
	}

	for _, lang := range ordered {
		track, cues, err := s.repo.FindReady(ctx, assetItem.ID, lang, kind)
		if err != nil {
			s.log.Warn("texttracks.subtitles: find ready track failed",
				zap.String("asset_id", assetItem.ID),
				zap.String("lang", lang),
				zap.Error(err))
			rep.Failed[lang] = err.Error()
			continue
		}
		if track == nil || len(cues) == 0 {
			// Text readiness without timed cues is not subtitle
			// readiness; the backfill's acquisition step owns repairing
			// that, not this one.
			continue
		}
		if _, mErr := s.subMaterializer.Materialize(ctx, SubtitleMaterializerInput{
			AssetID:         assetItem.ID,
			DriveFilename:   assetItem.Filename,
			LanguageCode:    lang,
			TextTrackID:     track.ID,
			ClipDurationMs:  assetItem.Duration.Milliseconds(),
			TimedCues:       cues,
			SubtitleStyleID: SubtitleArtifactStyleID,
			ClipContentHash: clipContentHash,
			DriveFolderID:   driveFolderID,
		}); mErr != nil {
			s.log.Warn("texttracks.subtitles: ASS materialization failed",
				zap.String("asset_id", assetItem.ID),
				zap.String("lang", lang),
				zap.Error(mErr))
			rep.Failed[lang] = mErr.Error()
			continue
		}
		rep.Delivered++
	}

	if len(rep.Failed) == 0 {
		rep.Failed = nil
	}
	return rep, nil
}
