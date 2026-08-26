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
// godlike/07 NO-FAKE-AVAILABILITY + Fase 4 STRICT contract:
// transcripts are read EXCLUSIVELY from `asset_text_tracks` via
// the TextTrackReader port. There is NO `metadata_json["transcript"]`
// / `metadata_json["clean_transcript"]` fallback path. There is
// NO `translation.TranslationPort` runtime invocation. A missing
// reader, a reader error, a non-READY track, or an empty language
// selection ALL surface the typed `*ErrTextTrackNotReady` to the
// caller — errors.As-probeable, struct-discriminated.

package usecase

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// resolveTranscript returns the canonical transcript string +
// the resolved *detail.TextTrack for the given clip via the
// TextTrackReader. There is no legacy metadata_json fallback
// (Fase 4 strict cutover) — every non-READY-or-missing path
// returns *ErrTextTrackNotReady.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): the
// signature is `(string, *detail.TextTrack, error)` so the
// per-clip accumulator can capture the resolved track (needed
// to populate the 3 ClipEvidence fingerprint fields:
// LanguageCode, TextTrackVersion, TranscriptHash). A nil track
// is the canonical signal that the 3 fingerprint fields are
// left empty. Strict cutover behavior: a missing/non-READY
// track returns *ErrTextTrackNotReady, errors.As-probeable —
// callers MUST handle this typed error explicitly. The
// `_ = clip` mark acknowledges that the legacy metadata_json
// read is RETIRED (Fase 4 hard cutover); the parameter is
// retained so BuildClipContext's call site does not drift.
//
// godlike/07 fail-closed: the typed error is returned to the
// caller and BuildClipContext propagates it without assembling
// evidence from an empty transcript.
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
) (string, *detail.TextTrack, error) {
	// Fase 4 strict cutover: textTrackReader is REQUIRED. A nil
	// reader is a composition-time wiring gap — surface
	// ErrTextTrackNotReady so the operator dashboard sees it
	// (no silent no-op, no metadata_json fallback).
	_ = clip // clip is unused after Fase 4 cutover — the legacy
	//          `metadata_json["transcript"]` read is RETIRED.
	if c.textTrackReader == nil {
		return "", nil, &ErrTextTrackNotReady{
			AssetID:            assetID,
			RequestedLanguage:  strings.TrimSpace(language),
			AvailableLanguages: nil,
			MissingKind:        detail.TextTrackTranscript,
		}
	}

	// Strict path: read the READY text track for the requested
	// (asset, language, kind) triple.
	lang := strings.TrimSpace(language)
	if lang == "" {
		return "", nil, &ErrTextTrackNotReady{
			AssetID:            assetID,
			RequestedLanguage:  "",
			AvailableLanguages: nil,
			MissingKind:        detail.TextTrackTranscript,
		}
	}

	track, _, err := c.textTrackReader.FindReady(ctx, assetID, lang, detail.TextTrackTranscript)
	if err != nil {
		c.logTextTrackResolveError(assetID, lang, err)
		return "", nil, &ErrTextTrackNotReady{
			AssetID:            assetID,
			RequestedLanguage:  lang,
			AvailableLanguages: nil,
			MissingKind:        detail.TextTrackTranscript,
		}
	}
	if track == nil {
		available := c.listReadyLanguagesBestEffort(ctx, assetID)
		return "", nil, &ErrTextTrackNotReady{
			AssetID:            assetID,
			RequestedLanguage:  lang,
			AvailableLanguages: available,
			MissingKind:        detail.TextTrackTranscript,
		}
	}
	// A previous render path could persist the editorial clip brief as if it
	// were a transcript. Never expose that contamination to the model.
	text := strings.ToLower(strings.TrimSpace(track.TextContent))
	if strings.Contains(text, "clip description:") || strings.Contains(text, "write a ") || strings.Contains(text, "source text:") {
		return "", nil, &ErrTextTrackNotReady{AssetID: assetID, RequestedLanguage: lang, MissingKind: detail.TextTrackTranscript}
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
	langs, err := c.textTrackReader.ListReadyLanguages(ctx, assetID, detail.TextTrackTranscript)
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

// Fase 4 strict cutover (PR-PY-CLIPS-CORRETTE-TRADOTTE, July 2026):
// the legacy `metadata_json["transcript"]` /
// `metadata_json["clean_transcript"]` read is RETIRED. There is
// no longer a function surface in this file for it; callers that
// require a per-clip transcript field MUST consult
// `asset_text_tracks` via the TextTrackReader. Operators backfill
// legacy clips via the Fase 5 admin command
// (cmd/admin/text_tracks_backfill.go) so the strict cutover is the
// single read path.
