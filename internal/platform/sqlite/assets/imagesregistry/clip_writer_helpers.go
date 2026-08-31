// Package assets — clip_writer_helpers.go consolidates the helper
// functions previously split across clip_atomic_writer_asset.go,
// clip_atomic_writer_outbox.go, clip_atomic_writer_tracks.go, and
// clip_atomic_writer_cues.go. These helpers are shared by
// SQLiteMediaCommitter (canonical_clip_writer.go) and the legacy
// ClipMetadataWriterAdapter.
//
// PR-SINGLE-WRITER (August 2026): the 8 clip_atomic_writer*.go files
// were eliminated after the migration of CommitClipAndIndexEvent +
// CommitClipTextAndIndexEvent onto SQLiteMediaCommitter. The helpers
// survived because they are consumed by multiple non-adapter callers
// (asset_committer.go, clip_metadata_writer.go).
package imagesregistry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// ── Column-mapping derivation helpers (from clip_atomic_writer_asset.go) ─

// clipTagsJSON marshals the clip tag list as a JSON array string for the
// media_assets.tags column (empty slice → NULL-compatible empty string).
func clipTagsJSON(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	raw, _ := json.Marshal(tags)
	return string(raw)
}

// clipTagsNorm derives the media_assets.tags_norm search string: the
// space-joined lowercase tag list (same convention as the image repo's
// normalizeTags). Empty for an empty tag list.
func clipTagsNorm(tags []string) string {
	var b strings.Builder
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strings.ToLower(t))
	}
	return b.String()
}

// deriveNameFromAsset returns a canonical name for the clip row.
// Pulls from asset.Metadata.Summary if non-empty, otherwise falls
// back to the asset ID.
func deriveNameFromAsset(asset youtubetypes.ClipAsset) string {
	if asset.Metadata.Summary != "" {
		return asset.Metadata.Summary
	}
	return ""
}

// deriveFilenameFromAsset returns the canonical filename for the
// clip row. Builds from the slug (asset.Metadata.Summary) if present,
// otherwise falls back to the canonical yt_<videoID>_<start>_<end>
// shape derived from the asset Coordinates. The full policy-versioned
// filename is set on the use case side via BuildClipFilename; the
// writer's filename is the basename of the local file when available.
func deriveFilenameFromAsset(asset youtubetypes.ClipAsset) string {
	if asset.LocalPath != "" {
		return filepathBase(asset.LocalPath)
	}
	return ""
}

// derivePolicyVersion extracts the policy_version suffix from a
// canonical clipID ("yt_<videoID>_<startSec>_<endSec>_<policyVer>").
// Returns "v1" when the suffix is missing.
func derivePolicyVersion(clipID string) string {
	const wantUnderscores = 4
	seen := 0
	for i := len(clipID) - 1; i >= 0; i-- {
		if clipID[i] == '_' {
			seen++
			if seen == wantUnderscores {
				pv := clipID[i+1:]
				if pv != "" {
					return pv
				}
				return "v1"
			}
		}
	}
	return "v1"
}

// deriveSourceVersion returns the canonical ingest-time content hash
// fingerprint used as event.source_version. In priority order:
//  1. asset.LegacyFileMD5 (the canonical MD5 of the local clip file).
//  2. fallback = MD5(clipID + ":" + policyVersion) — invariant under
//     retries so ON CONFLICT(event_key) collapses into a single row.
func deriveSourceVersion(clipID, fileHash, policyVersion string) string {
	if fileHash != "" {
		return fileHash
	}
	return checksum.LegacyMD5String(clipID + ":" + policyVersion)
}

// filepathBase is a thin wrapper around path/filepath.Base.
func filepathBase(p string) string {
	return filepath.Base(p)
}

// ── Outbox helpers (from clip_atomic_writer_outbox.go) ──────────────

// isTerminalOutboxStatus reports whether an outbox row's status is
// terminal (dead_letter or superseded).
func isTerminalOutboxStatus(status string) bool {
	return status == "dead_letter" || status == outboxevents.SupersedeStatus
}

// checkOutboxTerminalAfterCommit inspects the outbox enqueue result
// AFTER the orchestrator has called tx.Commit(). If the event was
// suppressed by an existing terminal row, this helper returns the
// BLOCKER #4 typed-error sentinel.
func checkOutboxTerminalAfterCommit(
	log *zap.Logger,
	inserted bool,
	clipID string,
	eventKey string,
	existingStatus string,
) error {
	if inserted {
		return nil
	}
	if !isTerminalOutboxStatus(existingStatus) {
		return nil
	}
	err := fmt.Errorf("%w: clip %q event_key=%q suppressed by existing terminal row (status=%q)",
		youtubeports.ErrOutboxTerminalConflict, clipID, eventKey, existingStatus)
	if log != nil {
		log.Warn("canonical clip writer: returning ErrOutboxTerminalConflict (BLOCKER #4 closure)",
			zap.String("clip_id", clipID),
			zap.String("event_key", eventKey),
			zap.String("existing_status", existingStatus),
			zap.Error(err))
	}
	return err
}

// ── Text track helpers (from clip_atomic_writer_tracks.go) ───────────

// localizedClipTextsToTextTracks converts payload-provided
// LocalizedClipText entries into domain TextTrack rows.
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
// (via RETURNING id) for each row.
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

// textTrackKey is the canonical key used to match TimedTextTrack
// entries with their parent TextTrack rows.
func textTrackKey(language string, kind detail.TextTrackKind, source detail.TextTrackSource) string {
	return strings.Join([]string{language, string(kind), string(source)}, "|")
}

// ── Cue segment helpers (from clip_atomic_writer_cues.go) ────────────

// insertTextTrackSegmentsInTx performs the BATCH INSERT of
// asset_text_track_segments, one row per cue.
func insertTextTrackSegmentsInTx(
	ctx context.Context,
	tx *sql.Tx,
	timedTracks []localized.TimedTextTrack,
	trackIDByKey map[string]int64,
) error {
	if len(timedTracks) == 0 {
		return nil
	}

	insertSQL := `
INSERT INTO asset_text_track_segments (
    track_id, sequence_no, start_ms, end_ms, text
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(track_id, sequence_no) DO UPDATE SET
    start_ms = excluded.start_ms,
    end_ms   = excluded.end_ms,
    text     = excluded.text`

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("insertTextTrackSegmentsInTx: prepare: %w", err)
	}
	defer stmt.Close()

	for _, tt := range timedTracks {
		key := textTrackKey(tt.LanguageCode, tt.TextKind, tt.SourceType)
		trackID, ok := trackIDByKey[key]
		if !ok {
			return fmt.Errorf("insertTextTrackSegmentsInTx: timed track has no matching TextTrack (lang=%s kind=%s source=%s) — ensure TextTracks has the parent row",
				tt.LanguageCode, tt.TextKind, tt.SourceType)
		}

		sortedCues := append([]detail.TimedCue(nil), tt.Cues...)
		sort.SliceStable(sortedCues, func(i, j int) bool {
			if sortedCues[i].StartMs != sortedCues[j].StartMs {
				return sortedCues[i].StartMs < sortedCues[j].StartMs
			}
			return sortedCues[i].EndMs < sortedCues[j].EndMs
		})

		for seq, cue := range sortedCues {
			if cue.StartMs < 0 || cue.EndMs < cue.StartMs || cue.Text == "" {
				return fmt.Errorf("insertTextTrackSegmentsInTx: invalid cue (seq=%d start=%d end=%d text_len=%d)",
					seq, cue.StartMs, cue.EndMs, len(cue.Text))
			}
			if _, execErr := stmt.ExecContext(ctx,
				trackID, seq+1, cue.StartMs, cue.EndMs, cue.Text,
			); execErr != nil {
				return fmt.Errorf("insertTextTrackSegmentsInTx: exec (seq=%d): %w", seq+1, execErr)
			}
		}
	}
	return nil
}
