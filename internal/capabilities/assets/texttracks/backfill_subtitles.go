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
	"strings"

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
	// UnTimed maps language → reason for a language that HAS text but no
	// timed cues, so no artifact could be built (and the alignment pass
	// could not repair it). Reported separately from Failed because the
	// text tracks are valid — this is "subtitle not ready", not "upload
	// failed". Named explicitly so a clip that loses nine of its ten
	// artifacts can never be mistaken for a complete one.
	UnTimed map[string]string `json:"untimed,omitempty"`
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
	rep := SubtitleDeliveryReport{Failed: map[string]string{}, UnTimed: map[string]string{}}

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

	// SUBTITLE READINESS (Sept 2026): a READY transcript that carries TEXT
	// but no timed cues cannot produce a subtitle artifact. The materializer
	// builds every translated track from the source transcript's text, so the
	// translations arrive without cues while the source language has them —
	// which is why the automatic fan-out delivered ONE .ass (the source
	// language) and left the other nine languages with a transcript row and
	// no subtitle file. Projecting a translated full text onto the SOURCE
	// timing is the canonical CuesWithText invariant (the same helper the
	// admin `text-tracks-align-cues` pass uses); applying it here is what
	// makes BOTH callers deliver one artifact per READY language.
	//
	// ReplaceTranscriptCues rewrites the WHOLE asset's transcript cues, so the
	// source language is always carried in the SAME batch as the languages
	// being aligned — a per-language call would delete the cues it is not
	// re-sending.
	if s.cues != nil {
		_, srcCues, srcErr := s.repo.FindReady(ctx, assetItem.ID, sourceLanguage, kind)
		switch {
		case srcErr != nil:
			s.log.Warn("texttracks.subtitles: source cues lookup failed; skipping cue alignment",
				zap.String("asset_id", assetItem.ID), zap.Error(srcErr))
		case len(srcCues) == 0:
			// No source timing to project onto: nothing to align.
		default:
			aligned := map[string][]detail.TimedCue{}
			timingFaithful := 0
			for _, lang := range ordered {
				if lang == sourceLanguage {
					continue
				}
				track, cues, fErr := s.repo.FindReady(ctx, assetItem.ID, lang, kind)
				if fErr != nil || track == nil || len(cues) > 0 || track.TextContent == "" {
					continue
				}
				// PREFERRED: translate each SOURCE cue and keep its window
				// (1:1). This is the correct subtitle alignment; the whole-text
				// distribution below is the degraded fallback because it slices
				// an already-translated transcript by word count and can split a
				// sentence across two cues.
				if s.cueTranslator != nil {
					if perCue, _, tErr := s.cueTranslator.Translate(ctx, srcCues, lang); tErr == nil && len(perCue) == len(srcCues) {
						aligned[lang] = perCue
						timingFaithful++
						continue
					} else if tErr != nil {
						s.log.Warn("texttracks.subtitles: per-cue translation failed; falling back to whole-text distribution",
							zap.String("asset_id", assetItem.ID),
							zap.String("lang", lang),
							zap.Error(tErr))
					}
				}
				aligned[lang] = CuesWithText(srcCues, track.TextContent)
			}
			if timingFaithful > 0 {
				s.log.Info("texttracks.subtitles: aligned translated cues with per-cue translation",
					zap.String("asset_id", assetItem.ID),
					zap.Int("languages", timingFaithful),
					zap.Int("source_cues", len(srcCues)))
			}
			if len(aligned) > 0 {
				batch := make(map[string][]detail.TimedCue, len(aligned)+1)
				batch[sourceLanguage] = srcCues
				for l, c := range aligned {
					batch[l] = c
				}
				if wErr := s.cues.ReplaceTranscriptCues(ctx, assetItem.ID, batch); wErr != nil {
					s.log.Warn("texttracks.subtitles: cue alignment write failed; languages stay text-only",
						zap.String("asset_id", assetItem.ID),
						zap.Int("aligned_languages", len(aligned)),
						zap.Error(wErr))
				} else {
					s.log.Info("texttracks.subtitles: aligned translated cues onto source timing",
						zap.String("asset_id", assetItem.ID),
						zap.Int("languages", len(aligned)),
						zap.Int("source_cues", len(srcCues)))
				}
			}
		}
	}

	clipContentHash := assetContentHash(assetItem)
	if clipContentHash == "" {
		clipContentHash = assetItem.ID
	}

	driveFolderID := assetItem.FolderID()
	if driveFolderID == "" {
		driveFolderID = s.driveFolderID
	}
	// ONE owner of the subtitle Drive layout. When the resolver is wired the
	// artifact is co-located with the transcript sidecar that the extraction
	// already uploaded (<subtitle root>/<videoID>/), instead of being nested
	// under the clip's media folder. A resolution failure degrades to the
	// asset folder (the text tracks are already durable; dropping the whole
	// delivery would be worse than one misplaced artifact).
	var driveSubpath []string
	if s.subtitleFolders != nil {
		loc, rErr := s.subtitleFolders.ResolveSubtitleLocation(ctx, assetItem.ID, assetItem.GetMetadataString("source_video_id"))
		switch {
		case rErr != nil:
			s.log.Warn("texttracks.subtitles: subtitle destination resolution failed; using the asset folder",
				zap.String("asset_id", assetItem.ID), zap.Error(rErr))
		case strings.TrimSpace(loc.FolderID) != "":
			driveFolderID = loc.FolderID
			driveSubpath = loc.Subpath
			if driveSubpath == nil {
				// Empty (non-nil) subpath: publish straight into the root,
				// next to the .txt sidecar.
				driveSubpath = []string{}
			}
		}
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
		if track == nil {
			// The language was never materialized for this asset: there is no
			// track to repair and no artifact to build.
			continue
		}
		if len(cues) == 0 {
			// FAIL-HONEST (Sept 2026): text readiness is NOT subtitle
			// readiness. The alignment pass above repairs the common case
			// (translated full text + source timing); when it could not —
			// no cue writer wired, a failed alignment write, or a source
			// transcript that itself carries no timing — the language is
			// REPORTED instead of silently dropped. This is the condition
			// that produced nine transcript rows and one .ass.
			rep.UnTimed[lang] = "no_timed_cues: READY transcript without cues cannot produce a subtitle artifact"
			s.log.Warn("texttracks.subtitles: language has text but no timed cues; no artifact generated",
				zap.String("asset_id", assetItem.ID),
				zap.String("lang", lang),
				zap.Int("text_chars", len(track.TextContent)))
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
			DriveSubpath:    driveSubpath,
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
	if len(rep.UnTimed) == 0 {
		rep.UnTimed = nil
	}
	return rep, nil
}
