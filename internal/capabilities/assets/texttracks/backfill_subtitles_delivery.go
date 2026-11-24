package texttracks

import (
	"context"
	"strings"

	"go.uber.org/zap"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// MaterializeSubtitleArtifacts generates and delivers one .ass subtitle
// artifact per READY language for a single asset. It is idempotent and a
// no-op (not an error) when the asset does not take subtitles.
func (s *BackfillService) MaterializeSubtitleArtifacts(
	ctx context.Context,
	assetItem *asset.Asset,
	sourceLanguage string,
	targetLanguages []string,
	kind detail.TextTrackKind,
) (SubtitleDeliveryReport, error) {
	rep := SubtitleDeliveryReport{Failed: map[string]string{}, UnTimed: map[string]string{}}

	if reason := subtitleDeliverySkipReason(s, assetItem, kind); reason != "" {
		rep.Skipped = true
		rep.SkipReason = reason
		return rep, nil
	}

	ordered := s.subtitleDeliveryLanguages(ctx, assetItem, sourceLanguage, targetLanguages, kind)
	rep.Languages = ordered
	s.alignSubtitleCues(ctx, assetItem, sourceLanguage, kind, ordered)

	clipContentHash := subtitleClipContentHash(assetItem)
	driveFolderID, driveSubpath := s.resolveSubtitleDestination(ctx, assetItem)
	for _, lang := range ordered {
		s.deliverSubtitleLanguage(ctx, assetItem, lang, kind, clipContentHash, driveFolderID, driveSubpath, &rep)
	}

	finalizeSubtitleDeliveryReport(&rep)
	return rep, nil
}

func subtitleDeliverySkipReason(s *BackfillService, assetItem *asset.Asset, kind detail.TextTrackKind) string {
	if s == nil || assetItem == nil || assetItem.ID == "" {
		return "no_asset"
	}
	if s.subMaterializer == nil {
		return "no_subtitle_materializer"
	}
	if !detail.RequiresSubtitles(string(assetItem.Source)) {
		return "source_does_not_take_subtitles"
	}
	if kind != detail.TextTrackTranscript {
		return "text_kind_is_not_transcript"
	}
	return ""
}

func (s *BackfillService) subtitleDeliveryLanguages(
	ctx context.Context,
	assetItem *asset.Asset,
	sourceLanguage string,
	targetLanguages []string,
	kind detail.TextTrackKind,
) []string {
	languages := append([]string{sourceLanguage}, targetLanguages...)
	readyLanguages, langErr := s.repo.ListReadyLanguages(ctx, assetItem.ID, kind)
	if langErr != nil {
		s.log.Warn("texttracks.subtitles: list ready languages failed",
			zap.String("asset_id", assetItem.ID), zap.Error(langErr))
	} else {
		languages = append(languages, readyLanguages...)
	}

	seen := make(map[string]bool)
	ordered := make([]string, 0, len(languages))
	for _, lang := range languages {
		if lang == "" || seen[lang] {
			continue
		}
		seen[lang] = true
		ordered = append(ordered, lang)
	}
	return ordered
}

func (s *BackfillService) alignSubtitleCues(
	ctx context.Context,
	assetItem *asset.Asset,
	sourceLanguage string,
	kind detail.TextTrackKind,
	ordered []string,
) {
	if s.cues == nil {
		return
	}
	_, srcCues, srcErr := s.repo.FindReady(ctx, assetItem.ID, sourceLanguage, kind)
	switch {
	case srcErr != nil:
		s.log.Warn("texttracks.subtitles: source cues lookup failed; skipping cue alignment",
			zap.String("asset_id", assetItem.ID), zap.Error(srcErr))
	case len(srcCues) == 0:
		// No source timing to project onto: nothing to align.
	default:
		aligned := map[string][]detail.TimedCue{}
		preserved := map[string][]detail.TimedCue{sourceLanguage: append([]detail.TimedCue(nil), srcCues...)}
		timingFaithful := 0
		for _, lang := range ordered {
			if lang == sourceLanguage {
				continue
			}
			cues, faithful, existing := s.alignedCuesForLanguage(ctx, assetItem, lang, kind, srcCues)
			if cues == nil {
				continue
			}
			if existing {
				preserved[lang] = cues
				continue
			}
			aligned[lang] = cues
			if faithful {
				timingFaithful++
			}
		}
		if timingFaithful > 0 {
			s.log.Info("texttracks.subtitles: aligned translated cues with per-cue translation",
				zap.String("asset_id", assetItem.ID),
				zap.Int("languages", timingFaithful),
				zap.Int("source_cues", len(srcCues)))
		}
		s.writeAlignedCues(ctx, assetItem, preserved, aligned, len(srcCues))
	}
}

func (s *BackfillService) alignedCuesForLanguage(
	ctx context.Context,
	assetItem *asset.Asset,
	lang string,
	kind detail.TextTrackKind,
	srcCues []detail.TimedCue,
) ([]detail.TimedCue, bool, bool) {
	track, cues, err := s.repo.FindReady(ctx, assetItem.ID, lang, kind)
	if err != nil || track == nil {
		return nil, false, false
	}
	if len(cues) > 0 {
		return append([]detail.TimedCue(nil), cues...), false, true
	}
	if track.TextContent == "" {
		return nil, false, false
	}
	// PREFERRED: translate each SOURCE cue and keep its window (1:1). The
	// whole-text distribution below is the degraded fallback because it slices
	// an already-translated transcript by word count.
	if s.cueTranslator != nil {
		if perCue, _, tErr := s.cueTranslator.Translate(ctx, srcCues, lang); tErr == nil && len(perCue) == len(srcCues) {
			return perCue, true, false
		} else if tErr != nil {
			s.log.Warn("texttracks.subtitles: per-cue translation failed; falling back to whole-text distribution",
				zap.String("asset_id", assetItem.ID),
				zap.String("lang", lang),
				zap.Error(tErr))
		}
	}
	return CuesWithText(srcCues, track.TextContent), false, false
}

func (s *BackfillService) writeAlignedCues(
	ctx context.Context,
	assetItem *asset.Asset,
	preserved map[string][]detail.TimedCue,
	aligned map[string][]detail.TimedCue,
	sourceCueCount int,
) {
	if len(aligned) == 0 {
		return
	}
	batch := make(map[string][]detail.TimedCue, len(preserved)+len(aligned))
	for lang, cues := range preserved {
		batch[lang] = cues
	}
	for lang, cues := range aligned {
		batch[lang] = cues
	}
	if err := s.cues.ReplaceTranscriptCues(ctx, assetItem.ID, batch); err != nil {
		s.log.Warn("texttracks.subtitles: cue alignment write failed; languages stay text-only",
			zap.String("asset_id", assetItem.ID),
			zap.Int("aligned_languages", len(aligned)),
			zap.Error(err))
		return
	}
	s.log.Info("texttracks.subtitles: aligned translated cues onto source timing",
		zap.String("asset_id", assetItem.ID),
		zap.Int("languages", len(aligned)),
		zap.Int("source_cues", sourceCueCount))
}

func subtitleClipContentHash(assetItem *asset.Asset) string {
	clipContentHash := assetContentHash(assetItem)
	if clipContentHash == "" {
		return assetItem.ID
	}
	return clipContentHash
}

func (s *BackfillService) resolveSubtitleDestination(ctx context.Context, assetItem *asset.Asset) (string, []string) {
	driveFolderID := assetItem.FolderID()
	if driveFolderID == "" {
		driveFolderID = s.driveFolderID
	}
	var driveSubpath []string
	if s.subtitleFolders == nil {
		return driveFolderID, driveSubpath
	}
	loc, err := s.subtitleFolders.ResolveSubtitleLocation(ctx, assetItem.ID, assetItem.GetMetadataString("source_video_id"))
	switch {
	case err != nil:
		s.log.Warn("texttracks.subtitles: subtitle destination resolution failed; using the asset folder",
			zap.String("asset_id", assetItem.ID), zap.Error(err))
	case strings.TrimSpace(loc.FolderID) != "":
		driveFolderID = loc.FolderID
		driveSubpath = loc.Subpath
		if driveSubpath == nil {
			driveSubpath = []string{}
		}
	}
	return driveFolderID, driveSubpath
}

func (s *BackfillService) deliverSubtitleLanguage(
	ctx context.Context,
	assetItem *asset.Asset,
	lang string,
	kind detail.TextTrackKind,
	clipContentHash, driveFolderID string,
	driveSubpath []string,
	rep *SubtitleDeliveryReport,
) {
	track, cues, err := s.repo.FindReady(ctx, assetItem.ID, lang, kind)
	if err != nil {
		s.log.Warn("texttracks.subtitles: find ready track failed",
			zap.String("asset_id", assetItem.ID),
			zap.String("lang", lang),
			zap.Error(err))
		rep.Failed[lang] = err.Error()
		return
	}
	if track == nil {
		return
	}
	if len(cues) == 0 {
		rep.UnTimed[lang] = "no_timed_cues: READY transcript without cues cannot produce a subtitle artifact"
		s.log.Warn("texttracks.subtitles: language has text but no timed cues; no artifact generated",
			zap.String("asset_id", assetItem.ID),
			zap.String("lang", lang),
			zap.Int("text_chars", len(track.TextContent)))
		return
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
		DriveSubpath:    driveSubpath,
	}); mErr != nil {
		s.log.Warn("texttracks.subtitles: ASS materialization failed",
			zap.String("asset_id", assetItem.ID),
			zap.String("lang", lang),
			zap.Error(mErr))
		rep.Failed[lang] = mErr.Error()
		return
	}
	rep.Delivered++
}

func finalizeSubtitleDeliveryReport(rep *SubtitleDeliveryReport) {
	if len(rep.Failed) == 0 {
		rep.Failed = nil
	}
	if len(rep.UnTimed) == 0 {
		rep.UnTimed = nil
	}
}
