// Package assets — clip_atomic_writer_cues.go: asset_text_track_segments
// BATCH INSERT helper. Split from the orchestrator clip_atomic_writer.go
// for per-table responsibility (clip_atomic_writer split, July 2026).
//
// godlike/06 SSOT (single tx invariant): this file accepts *sql.Tx
// as a parameter and NEVER opens its own transaction. The tx is
// owned by the orchestrator (clip_atomic_writer.go), which calls
// us once after upsertTextTracksReturningIDsInTx has populated the
// track-id match map.
//
// godlike/10 SSOT (language provenance non-duplication): the match-key
// resolution for orphan-timed-track rejection uses `textTrackKey()`
// from clip_atomic_writer_tracks.go — the canonical key shape
// (language_code + "|" + text_kind + "|" + source_type). The
// helper does NOT inline a hand-rolled key formatter.
//
// godlike/10 SSOT (sequence_no monotonic): sequence_no is assigned
// in-memory by this function BEFORE the INSERT lands. The DB has a
// UNIQUE(track_id, sequence_no) constraint, so we sort cues
// ascending by StartMs and assign 1-based sequence_no to keep
// persistence order stable across retries. Equal-start cues use
// SliceStable so caller order is preserved (deterministic by-key
// tie-break).
package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// insertTextTrackSegmentsInTx performs the BATCH INSERT of
// asset_text_track_segments, one row per cue. Cues are sorted
// ascending by StartMs BEFORE assigning sequence_no (UNIQUE
// constraint enforcement). Each TimedTextTrack MUST resolve to
// a parent text track via trackIDByKey; the writer surfaces a
// typed error when no match is found.
//
// godlike/06 SSOT: sequence_no is assigned in-memory by this
// function. The DB has a UNIQUE(track_id, sequence_no) constraint;
// the writer also avoids negative or non-monotonic sequence_no so
// the persistence order is stable across retries.
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

		// Sort cues ascending by StartMs so sequence_no is
		// monotonic. Use SliceStable so equal-start cues preserve
		// caller order (deterministic behaviour across retries).
		sortedCues := append([]asset.TimedCue(nil), tt.Cues...)
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
