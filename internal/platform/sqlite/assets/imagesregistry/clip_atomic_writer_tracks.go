// Package assets — clip_atomic_writer_tracks.go: asset_text_tracks
// UPSERT (with RETURNING id) + match-key builder + LocalizedClipText
// → TextTrack domain conversion. Split from the orchestrator
// clip_atomic_writer.go for per-table responsibility (clip_atomic_writer
// split, July 2026).
//
// godlike/06 SSOT (single tx invariant): this file accepts *sql.Tx
// as a parameter and NEVER opens its own transaction. The tx is
// owned by the orchestrator (clip_atomic_writer.go). The helper
// surface here is the LOCALIZED stripe; the legacy non-RETURNING
// `upsertTextTracksInTx` lives in clip_metadata_writer.go (a sibling)
// and is called unchanged by the orchestrator's step-(2.5).
// We do NOT duplicate the upsert SQL here — the localized variant
// only differs by appending `RETURNING id` so the segments BATCH
// INSERT in cues.go can resolve parent FKs.
//
// godlike/10 SSOT (language provenance non-duplication): the
// match-key (language + text_kind + source_type) lives ONCE here as
// `textTrackKey`. The cues BATCH INSERT in clip_atomic_writer_cues.go
// reuses this exact key to resolve orphan-timed-track rejection.
package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// localizedClipTextsToTextTracks converts payload-provided
// LocalizedClipText entries into domain TextTrack rows suitable
// for upsertTextTracksInTx. Each non-empty text field
// (Transcript, Description, Summary, Title) produces a separate
// TextTrack entry with the corresponding TextKind.
//
// godlike/10 SSOT: this is the SOLE place where the LocalizedClipText
// → TextTrack structural conversion lives. Both entry points
// (CommitClipAndIndexEvent legacy stripe at step 2.5; custom callers
// of asset_text_tracks on other write paths) reuse this same function.
// Provenance (source_type, is_original) is derived once here and never
// re-derived inline at the call sites.
func localizedClipTextsToTextTracks(clipID string, texts []youtubetypes.LocalizedClipText) []detail.TextTrack {
	if len(texts) == 0 {
		return nil
	}
	var tracks []detail.TextTrack
	for _, t := range texts {
		lang := t.LanguageCode
		if lang == "" {
			lang = "en"
		}
		srcType := detail.TextTrackSource(t.SourceType)
		if srcType == "" {
			srcType = detail.TextSourceProvided
		}
		isOriginal := t.IsOriginal
		if srcType == detail.TextSourceProvided {
			isOriginal = true
		}

		type entry struct {
			kind    detail.TextTrackKind
			content string
		}
		entries := []entry{
			{detail.TextTrackTranscript, t.Transcript},
			{"description", t.Description},
			{"summary", t.Summary},
			{"title", t.Title},
		}
		for _, e := range entries {
			if e.content == "" {
				continue
			}
			var confidence *float64
			if t.Confidence > 0 {
				confidence = &t.Confidence
			}
			tracks = append(tracks, detail.TextTrack{
				AssetID:            clipID,
				LanguageCode:       lang,
				TextKind:           e.kind,
				TextContent:        e.content,
				SourceType:         srcType,
				SourceLanguageCode: t.SourceLanguageCode,
				IsOriginal:         isOriginal,
				ModelName:          t.ModelName,
				ModelVersion:       t.ModelVersion,
				Confidence:         confidence,
				Status:             detail.TextTrackReady,
			})
		}
	}
	return tracks
}

// upsertTextTracksReturningIDsInTx performs the asset_text_tracks
// UPSERT inside the caller's tx, capturing the assigned track_id
// (via RETURNING id) for each row. The returned map is keyed by
// (language_code + "|" + text_kind + "|" + source_type) so the
// step-(4) segments batch INSERT in clip_atomic_writer_cues.go can
// resolve parent FKs.
//
// godlike/06 SSOT: the upsert SQL mirrors upsertTextTracksInTx
// (clip_metadata_writer.go) but adds the RETURNING clause. The
// hash + source_version columns are populated from the row's
// TextHash / SourceVersion fields; callers MUST have invoked the
// canonical hash factory (internal/kernel/asset/text_track_hashes.go).
// Re-deriving the SHA-256 inline is forbidden (see the SSOT
// contract on text_track_hashes.go).
func upsertTextTracksReturningIDsInTx(
	ctx context.Context,
	tx *sql.Tx,
	tracks []detail.TextTrack,
	nowStr string,
) (map[string]int64, error) {
	trackIDByKey := make(map[string]int64, len(tracks))
	if len(tracks) == 0 {
		return trackIDByKey, nil
	}

	upsertSQL := `
INSERT INTO asset_text_tracks (
    asset_id, language_code, text_kind,
    text_content,
    source_type, source_language_code, is_original,
    provider, model_name, model_version,
    text_hash, source_version,
    confidence, status,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
ON CONFLICT(asset_id, language_code, text_kind) WHERE is_current = 1 DO UPDATE SET
    text_content         = excluded.text_content,
    source_type          = excluded.source_type,
    source_language_code = excluded.source_language_code,
    is_original          = excluded.is_original,
    provider             = excluded.provider,
    model_name           = excluded.model_name,
    model_version        = excluded.model_version,
    text_hash            = excluded.text_hash,
    source_version       = excluded.source_version,
    confidence           = excluded.confidence,
    status               = excluded.status,
    updated_at           = datetime('now')
RETURNING id`

	stmt, err := tx.PrepareContext(ctx, upsertSQL)
	if err != nil {
		return nil, fmt.Errorf("upsertTextTracksReturningIDsInTx: prepare: %w", err)
	}
	defer stmt.Close()

	for _, t := range tracks {
		if t.AssetID == "" || t.LanguageCode == "" || t.TextKind == "" {
			return nil, fmt.Errorf("upsertTextTracksReturningIDsInTx: row missing required keys (AssetID/LanguageCode/TextKind)")
		}

		var confidence interface{}
		if t.Confidence != nil {
			confidence = *t.Confidence
		}

		isOriginal := 0
		if t.IsOriginal {
			isOriginal = 1
		}
		status := string(t.Status)
		if status == "" {
			status = string(detail.TextTrackReady)
		}

		var id int64
		scanErr := stmt.QueryRowContext(ctx,
			t.AssetID,
			t.LanguageCode,
			string(t.TextKind),
			t.TextContent,
			string(t.SourceType),
			t.SourceLanguageCode,
			isOriginal,
			t.Provider,
			t.ModelName,
			t.ModelVersion,
			t.TextHash,
			t.SourceVersion,
			confidence,
			status,
		).Scan(&id)
		if scanErr != nil {
			return nil, fmt.Errorf("upsertTextTracksReturningIDsInTx: exec (asset=%s lang=%s kind=%s): %w",
				t.AssetID, t.LanguageCode, t.TextKind, scanErr)
		}

		key := textTrackKey(t.LanguageCode, t.TextKind, t.SourceType)
		trackIDByKey[key] = id
	}
	return trackIDByKey, nil
}

// textTrackKey is the canonical key used by the writer to match
// TimedTextTrack entries with their parent TextTrack rows. The
// canonical key shape is (language_code + "|" + text_kind + "|" +
// source_type) — three fields are required because source_type is
// part of the unique-write contract for the asset_text_tracks
// table (a clip may have multiple tracks per (lang, kind) if
// they come from different sources; e.g. a user-provided
// transcript AND a YouTube-subtitle generated track).
//
// godlike/10 SSOT (language provenance non-duplication): this key
// is the canonical match-key shared between the tracks and cues
// helpers. Both clip_atomic_writer_tracks.go (RETURNING-id) and
// clip_atomic_writer_cues.go (segments BATCH INSERT) call this
// exact function. No caller inlines a hand-rolled key.
func textTrackKey(language string, kind detail.TextTrackKind, source detail.TextTrackSource) string {
	return strings.Join([]string{language, string(kind), string(source)}, "|")
}
