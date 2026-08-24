// Package texttracks — backfill_acquire.go (LEAF):
//
// Acquisition+save leaf for the backfill pipeline. Wraps the
// AcquireService (priorities 2-5: local VTT/SRT → YouTube subs
// → Whisper) with a "save-on-success" rule that writes the
// acquired text as a READY source track via repo.UpsertBatch.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// "fill the source-language gap" decision. Pure leaf helpers
// (extractLocalPath, extractVideoID, acquiredFromLabel,
// providerFromSourceType) live here because they're the
// metadata+canonical-mapping companions of the acquisition
// flow. ProcessAsset and Run delegate to these helpers
// instead of re-implementing them.
//
// Cross-file callers (same package):
//   - backfill_go       : declares BackfillService struct +
//     BackfillAssetResult that the
//     tryAcquire results flow into.
//   - backfill_process.go : invokes tryAcquire on the
//     ErrNoSourceTrack branch (when the
//     source is missing AND the acquirer
//     is wired); consumes acquiredFromLabel
//     to populate the AcquiredFrom field.
package texttracks

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// tryAcquire is the Fase 5 helper: runs the AcquireService chain
// (priorities 2-5) and saves the result as a READY source track.
// Returns (result, nil) on success; (nil, err) when acquisition
// fails.
//
// godlike/06 SSOT: the per-clip acquisition decision is owned
// by the BackfillService. The AcquireService is a
// "find-or-fail" primitive; the BackfillService is the
// "save-on-success" wrapper.
func (s *BackfillService) tryAcquire(
	ctx context.Context,
	assetItem *asset.Asset,
	opts BackfillOptions,
) (*AcquireResult, error) {
	var acqResult *AcquireResult
	var err error
	// A metadata summary is an honest source only for the summary kind. It
	// keeps an asset searchable when the binary source is unavailable without
	// mislabelling descriptive text as a transcript.
	if opts.TextKind == asset.TextTrackSummary && assetItem.ClipSummary() != "" {
		acqResult = &AcquireResult{
			AssetID:      assetItem.ID,
			PlainText:    assetItem.ClipSummary(),
			LanguageCode: opts.SourceLanguage,
			SourceType:   asset.TextSourceProvided,
			Priority:     0,
		}
	} else {
		localPath := extractLocalPath(assetItem)
		videoID := extractVideoID(assetItem)
		if localPath == "" && videoID == "" {
			return nil, fmt.Errorf("backfill: cannot acquire — no local_path or video_id on asset %s", assetItem.ID)
		}
		acqResult, err = s.acquirer.Acquire(ctx, AcquireCommand{
			AssetID:     assetItem.ID,
			VideoID:     videoID,
			LocalPath:   localPath,
			DriveFileID: extractDriveFileID(assetItem),
			Language:    opts.SourceLanguage,
		})
		if err != nil {
			return nil, err
		}
	}
	// Save the acquired text as a READY source track. Uses
	// the canonical text-hash factory + source-version
	// computation. The track is upserted on
	// UNIQUE(asset_id, language_code, text_kind) so the
	// operation is idempotent.
	lang := acqResult.LanguageCode
	if lang == "" {
		lang = opts.SourceLanguage
	}
	hash := asset.TextHash(acqResult.PlainText, lang, opts.TextKind)
	srcType := acqResult.SourceType
	// Convert the priority-2 "local_file" sentinel to the
	// canonical asset.TextSourceProvided (local files are
	// treated as user-provided provenance).
	if srcType == "local_file" {
		srcType = asset.TextSourceProvided
	}
	provider := providerFromSourceType(acqResult.SourceType)
	track := asset.TextTrack{
		AssetID:            assetItem.ID,
		LanguageCode:       lang,
		TextKind:           opts.TextKind,
		TextContent:        acqResult.PlainText,
		SourceType:         srcType,
		SourceLanguageCode: lang,
		IsOriginal:         true,
		Provider:           provider,
		TextHash:           hash,
		SourceVersion:      asset.SourceVersion(hash, lang, lang, provider, "", "", ""),
		Confidence:         acqResult.Confidence,
		Status:             asset.TextTrackReady,
	}
	if err := s.repo.UpsertBatch(ctx, []asset.TextTrack{track}); err != nil {
		return acqResult, fmt.Errorf("backfill: save acquired source track: %w", err)
	}
	if len(acqResult.Cues) > 0 && s.cues != nil {
		if err := s.cues.ReplaceTranscriptCues(ctx, assetItem.ID, map[string][]asset.TimedCue{lang: acqResult.Cues}); err != nil {
			return acqResult, fmt.Errorf("backfill: save acquired cues: %w", err)
		}
	}
	s.log.Info("backfill: acquired source track and cues saved",
		zap.String("asset_id", assetItem.ID),
		zap.String("language", lang),
		zap.String("source_type", string(acqResult.SourceType)),
		zap.Int("priority", acqResult.Priority),
		zap.Int("cues_count", len(acqResult.Cues)))
	return acqResult, nil
}

func extractDriveFileID(a *asset.Asset) string {
	if a == nil {
		return ""
	}
	return a.DriveFileID()
}

// extractLocalPath reads the canonical local_path accessor from the
// asset. Falls back to Filename when no local path was persisted.
//
// godlike/06 SSOT: the metadata→local_path extraction lives
// ONLY here (youTube-pipeline metadata shape is owned
// canonically by this file). Other leaves MUST delegate.
func extractLocalPath(a *asset.Asset) string {
	if a == nil {
		return ""
	}
	if localPath := a.LocalPath(); localPath != "" {
		return localPath
	}
	return a.Filename
}

// extractVideoID reads the YouTube video ID through the canonical
// provenance accessors first. `source_id` / `video_id` remain legacy
// fallbacks for clips written before provenance-key convergence; URL
// parsing also retains the historical `url` alias for old rows.
//
// godlike/06 SSOT: the metadata→video_id extraction lives ONLY
// here. Inline lookups in other leaves would corrode the canon.
func extractVideoID(a *asset.Asset) string {
	if a == nil {
		return ""
	}
	if v := a.MetadataSourceVideoID(); v != "" {
		return v
	}
	if a.Metadata != nil {
		if v, ok := a.Metadata["source_id"].(string); ok && v != "" {
			return v
		}
		if v, ok := a.Metadata["video_id"].(string); ok && v != "" {
			return v
		}
		for _, raw := range []string{a.MetadataSourceURL(), a.YouTubeURL(), a.GetMetadataString("url")} {
			if id := youtubeVideoID(raw); id != "" {
				return id
			}
		}
	}
	if id := youtubeVideoID(a.SourceURL); id != "" {
		return id
	}
	// Fallback: strip the "yt_" prefix from the asset ID
	// (canonical clip ID format: yt_<videoID>_<start>_<end>_<policy>).
	if len(a.ID) > 3 && a.ID[:3] == "yt_" {
		// Find the second underscore.
		for i := 3; i < len(a.ID); i++ {
			if a.ID[i] == '_' {
				return a.ID[3:i]
			}
		}
	}
	return ""
}

func youtubeVideoID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if id := strings.TrimSpace(u.Query().Get("v")); id != "" {
		return id
	}
	path := strings.Trim(u.Path, "/")
	if strings.HasPrefix(path, "shorts/") {
		return strings.TrimPrefix(path, "shorts/")
	}
	if strings.HasPrefix(path, "embed/") {
		return strings.TrimPrefix(path, "embed/")
	}
	return ""
}

// acquiredFromLabel maps the source_type to a human-readable
// label for the per-clip report.
//
// godlike/06 SSOT: the source_type→wire-label mapping is owned
// ONLY here (it's a pure mapping, not a decision). Tests can
// pin the labels via backfill_acquire_test.go if it ever
// materialises.
func acquiredFromLabel(st asset.TextTrackSource) string {
	switch st {
	case asset.TextSourceProvided:
		return "local_file"
	case asset.TextSourceYouTubeSubtitle:
		return "youtube_subtitle"
	case asset.TextSourceWhisper:
		return "whisper"
	default:
		return string(st)
	}
}

// providerFromSourceType returns a canonical Provider string
// for the acquired text-track's source_type. The Materializer
// uses Provider as part of the MaterializationKey.
//
// godlike/06 SSOT: the source_type→provider canonicalisation
// lives ONLY here. Both tryAcquire (twice: once for Provider,
// once for SourceVersion) AND the canonical MaterializationKey
// builder in materializer.go DELIVER the same string for the
// same source_type — no drift.
func providerFromSourceType(st asset.TextTrackSource) string {
	switch st {
	case asset.TextSourceYouTubeSubtitle:
		return "youtube"
	case asset.TextSourceWhisper:
		return "whisper"
	default:
		return ""
	}
}
