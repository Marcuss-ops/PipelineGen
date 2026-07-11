// Package usecase — clip_source_builder_transcript.go owns the
// transcript-resolution path for ClipSourceBuilder (Fase 4 cutover
// to TextTrackReader, PR-PY-CLIPS-CORRETTE-TRADOTTE, July 2026).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// per-clip transcript resolution logic. The main
// `clip_source_builder.go` loop delegates here via
// `c.resolveTranscript(ctx, assetID, language, clip)`; the per-clip
// transcript string is the canonical input to both the assembled
// source text and the per-clip ClipDetail.Transcript field.
//
// godlike/07 NO-FAKE-AVAILABILITY: the legacy
// `metadata_json["transcript"]` / `metadata_json["clean_transcript"]`
// read is RETIRED by default. The fallback is gated behind the
// `media.multilingual.migration_fallback_legacy_metadata` config
// flag (see MultilingualConfig.MigrationFallbackLegacyMetadata).
// When the flag is false (the post-cutover default), the legacy
// path is REMOVED: a missing/non-READY text track surfaces the
// typed `*ErrTextTrackNotReady` to the caller. When the flag is
// true (one-time migration window), the legacy path is the
// fallback so operators can keep pre-cutover clips rendered while
// the backfill CLI populates `asset_text_tracks`.

package usecase

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

// resolveTranscript returns the canonical transcript string +
// the resolved *asset.TextTrack for the given clip, preferring
// the `asset_text_tracks` row at the requested language (via
// TextTrackReader) and falling back to the legacy
// `metadata_json["transcript"]` /
// `metadata_json["clean_transcript"]` ONLY when the migration
// fallback flag is enabled.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): the
// signature is `(string, *asset.TextTrack, error)` so the
// per-clip accumulator can capture the resolved track (needed
// to populate the 3 new ClipEvidence fingerprint fields:
// LanguageCode, TextTrackVersion, TranscriptHash). The legacy
// path returns a nil track (the legacy metadata_json read has
// no TextTrack shape); the 3 fingerprint fields are left
// empty in that case (the pre-Fase-4 behavior).
//
// godlike/07 minimum-blast-radius: the typed error is returned
// to the caller (BuildClipContext logs it and continues with
// an empty transcript — strict-error propagation lands in a
// follow-up PR).
//
// Callers:
//   - BuildClipContext (uses both the transcript string and the
//     resolved track; calls resolveTranscript EXACTLY ONCE per
//     clip to avoid duplicate DB round-trips)
func (c *ClipSourceBuilder) resolveTranscript(
	ctx context.Context,
	assetID string,
	language string,
	clip *asset.Asset,
) (string, *asset.TextTrack, error) {
	// Legacy path: textTrackReader is not wired. Returns
	// ("", nil, nil) — the per-clip accumulator sees a nil
	// track and skips the 3 fingerprint fields.
	if c.textTrackReader == nil {
		return legacyMetadataTranscript(clip), nil, nil
	}

	// New path: read the READY text track for the requested
	// (asset, language, kind) triple.
	lang := strings.TrimSpace(language)
	if lang == "" {
		return "", nil, &ErrTextTrackNotReady{
			AssetID:           assetID,
			RequestedLanguage: "",
			AvailableLanguages: nil,
			MissingKind:       asset.TextTrackTranscript,
		}
	}

	track, _, err := c.textTrackReader.FindReady(ctx, assetID, lang, asset.TextTrackTranscript)
	if err != nil {
		if c.legacyFallback {
			return legacyMetadataTranscript(clip), nil, nil
		}
		c.logTextTrackResolveError(assetID, lang, err)
		return "", nil, &ErrTextTrackNotReady{
			AssetID:           assetID,
			RequestedLanguage: lang,
			AvailableLanguages: nil,
			MissingKind:       asset.TextTrackTranscript,
		}
	}
	if track == nil {
		if c.legacyFallback {
			return legacyMetadataTranscript(clip), nil, nil
		}
		available := c.listReadyLanguagesBestEffort(ctx, assetID)
		return "", nil, &ErrTextTrackNotReady{
			AssetID:           assetID,
			RequestedLanguage: lang,
			AvailableLanguages: available,
			MissingKind:       asset.TextTrackTranscript,
		}
	}

	// Track found and READY. Return both the track (for
	// fingerprint field population) and the flat text content
	// (for prompt construction).
	return track.TextContent, track, nil
}

// listReadyLanguagesBestEffort invokes ListReadyLanguages and
// returns the result, swallowing any error so the resolveTranscript
// path can always populate ErrTextTrackNotReady.AvailableLanguages
// when possible. godlike/07 minimum-blast-radius: a repo-level
// error here is not fatal — the caller can still surface the
// not-ready error with an empty available-list.
func (c *ClipSourceBuilder) listReadyLanguagesBestEffort(ctx context.Context, assetID string) []string {
	if c.textTrackReader == nil {
		return nil
	}
	langs, err := c.textTrackReader.ListReadyLanguages(ctx, assetID, asset.TextTrackTranscript)
	if err != nil {
		if c.log != nil {
			c.log.Warn("clip source builder: list ready languages failed",
				zap.String("asset_id", assetID),
				zap.Error(err))
		}
		return nil
	}
	return langs
}

// logTextTrackResolveError emits the canonical WARN log for a
// TextTrackReader error. Extracted so the resolveTranscript path
// stays linear and the log schema is uniform across the 2 error
// branches (repo error + not-found).
func (c *ClipSourceBuilder) logTextTrackResolveError(assetID, language string, err error) {
	if c.log == nil {
		return
	}
	c.log.Warn("clip source builder: text track resolve failed",
		zap.String("asset_id", assetID),
		zap.String("language", language),
		zap.Error(err))
}

// legacyMetadataTranscript is the pre-Fase-4 transcript read
// from `metadata_json["transcript"]` /
// `metadata_json["clean_transcript"]`. The function is package-
// private and is the SOLE remaining metadata_json transcript
// reader in the codebase (post-cutover). It is invoked ONLY
// when `MigrationFallbackLegacyMetadata` is true (the migration
// window); the production path (flag=false) NEVER calls it.
func legacyMetadataTranscript(clip *asset.Asset) string {
	if clip == nil {
		return ""
	}
	if t := clip.GetMetadataString("transcript"); t != "" {
		return t
	}
	return clip.GetMetadataString("clean_transcript")
}
