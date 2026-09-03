// Package media — clip_writer.go is the canonical YouTube clip writer on
// the PostgreSQL media SSOT. Ported during the media demolition
// (September 2026) from internal/platform/sqlite/assets/imagesregistry/
// canonical_clip_writer.go + clip_writer_helpers.go: the composite atomic
// writes (clip + text tracks + cue segments + outbox event) are assembly
// over the SAME PostgresMediaCommitter primitives (CommitTx, tx-scoped
// text-track upserts) that the SQLite committer used — one engine, one
// transaction, zero SQLite media writes.
//
// The request-translation / text-track / segment helpers live in
// clip_writer_helpers.go (split per the godlike/08 600-line gate).
package media

import (
	"context"
	"fmt"
	"sort"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// CommitClipAndIndexEvent is the canonical YouTube clip atomic write:
// media_assets + asset_text_tracks + outbox event in ONE PostgreSQL
// transaction, with the BLOCKER #4 typed error on terminal outbox
// conflicts (SQLite mirror: canonical_clip_writer.go).
func (c *PostgresMediaCommitter) CommitClipAndIndexEvent(
	ctx context.Context,
	clipID string,
	clipAsset youtubetypes.ClipAsset,
	event youtubeports.IndexEventPayload,
) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipAndIndexEvent: committer not wired")
	}
	if clipID == "" {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipAndIndexEvent: clipID is required")
	}
	if event.AggregateID == "" {
		event.AggregateID = clipID
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipAndIndexEvent: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	req, err := buildYouTubeCommitRequest(clipID, clipAsset)
	if err != nil {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipAndIndexEvent: build commit request: %w", err)
	}
	res, err := c.CommitTx(ctx, tx, req)
	if err != nil {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipAndIndexEvent: commit asset: %w", err)
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	if len(clipAsset.Texts) > 0 {
		tracks := localizedClipTextsToTextTracks(clipID, clipAsset.Texts)
		if len(tracks) > 0 {
			if err := upsertTextTracksInTxPG(ctx, tx, tracks, nowStr); err != nil {
				return fmt.Errorf("PostgresMediaCommitter.CommitClipAndIndexEvent: upsert text tracks: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipAndIndexEvent: commit: %w", err)
	}
	committed = true

	if terr := checkOutboxTerminalAfterCommitPG(c.log, res.OutboxInserted, clipID, res.OutboxEventKey, res.OutboxExistingStatus); terr != nil {
		return terr
	}

	if c.log != nil {
		c.log.Debug("PostgresMediaCommitter: clip + index event committed",
			zap.String("clip_id", clipID),
			zap.String("event_key", res.OutboxEventKey),
		)
	}
	return nil
}

// CommitClipTextAndIndexEvent is the canonical localized clip atomic
// write: media_assets + asset_text_tracks (RETURNING id) +
// asset_text_track_segments + outbox event, all in ONE PostgreSQL
// transaction (SQLite mirror: canonical_clip_writer.go).
func (c *PostgresMediaCommitter) CommitClipTextAndIndexEvent(
	ctx context.Context,
	cmd localized.CommitLocalizedClipCommand,
) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipTextAndIndexEvent: committer not wired")
	}
	if cmd.Clip.ID == "" {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipTextAndIndexEvent: cmd.Clip.ID is required")
	}

	if verr := validateLocalizedClipPolicy(cmd); verr != nil {
		if c.log != nil {
			c.log.Warn("PostgresMediaCommitter.CommitClipTextAndIndexEvent: locale policy violated; rolling back (no rows written)",
				zap.String("clip_id", cmd.Clip.ID), zap.Error(verr))
		}
		return verr
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipTextAndIndexEvent: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	nowStr := time.Now().UTC().Format(time.RFC3339)
	req, err := buildYouTubeCommitRequest(cmd.Clip.ID, cmd.Clip)
	if err != nil {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipTextAndIndexEvent: build commit request: %w", err)
	}
	res, err := c.CommitTx(ctx, tx, req)
	if err != nil {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipTextAndIndexEvent: commit asset: %w", err)
	}

	trackIDByKey, terr := upsertTextTracksReturningIDsInTxPG(ctx, tx, cmd.TextTracks, nowStr)
	if terr != nil {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipTextAndIndexEvent: upsert text tracks: %w", terr)
	}

	if serr := insertTextTrackSegmentsInTxPG(ctx, tx, cmd.TimedTracks, trackIDByKey); serr != nil {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipTextAndIndexEvent: insert segments: %w", serr)
	}

	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("PostgresMediaCommitter.CommitClipTextAndIndexEvent: commit: %w", cerr)
	}
	committed = true

	if terr := checkOutboxTerminalAfterCommitPG(c.log, res.OutboxInserted, cmd.Clip.ID, res.OutboxEventKey, res.OutboxExistingStatus); terr != nil {
		return terr
	}

	if c.log != nil {
		c.log.Debug("PostgresMediaCommitter.CommitClipTextAndIndexEvent: clip + tracks + segments + index event committed atomically",
			zap.String("clip_id", cmd.Clip.ID),
			zap.String("event_key", res.OutboxEventKey),
			zap.Int("text_tracks", len(cmd.TextTracks)),
			zap.Int("timed_tracks", len(cmd.TimedTracks)),
			zap.Bool("outbox_inserted", res.OutboxInserted))
	}
	return nil
}

// ReplaceTranscriptCues replaces all transcript cues for an asset's READY
// transcript tracks. Opens its own transaction (SQLite mirror:
// canonical_clip_writer.go).
func (c *PostgresMediaCommitter) ReplaceTranscriptCues(ctx context.Context, assetID string, byLang map[string][]detail.TimedCue) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("PostgresMediaCommitter.ReplaceTranscriptCues: committer not wired")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	rows, err := tx.QueryContext(ctx, `SELECT id, language_code FROM asset_text_tracks WHERE asset_id=$1 AND text_kind='transcript' AND status='READY' AND is_current=1`, assetID)
	if err != nil {
		return err
	}
	ids := map[string]int64{}
	for rows.Next() {
		var id int64
		var lang string
		if err := rows.Scan(&id, &lang); err != nil {
			rows.Close()
			return err
		}
		ids[lang] = id
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if _, err = tx.ExecContext(ctx, `DELETE FROM asset_text_track_segments WHERE track_id IN (SELECT id FROM asset_text_tracks WHERE asset_id=$1 AND text_kind='transcript' AND status='READY' AND is_current=1)`, assetID); err != nil {
		return err
	}
	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO asset_text_track_segments(track_id, sequence_no, start_ms, end_ms, text) VALUES($1,$2,$3,$4,$5)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, lang := range langs {
		id, ok := ids[lang]
		if !ok {
			return fmt.Errorf("ReplaceTranscriptCues: missing READY transcript %s", lang)
		}
		cues := append([]detail.TimedCue(nil), byLang[lang]...)
		sort.SliceStable(cues, func(i, j int) bool { return cues[i].StartMs < cues[j].StartMs })
		for i, cu := range cues {
			if cu.StartMs < 0 || cu.EndMs <= cu.StartMs {
				return fmt.Errorf("ReplaceTranscriptCues: invalid cue %s #%d", lang, i+1)
			}
			if _, err := stmt.ExecContext(ctx, id, i+1, cu.StartMs, cu.EndMs, cu.Text); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// UpdateFolderPathForClip is the FolderPathWriter port method: resolves
// source_version and delegates to the tx-bound folder path mutator + index
// request (SQLite mirror: canonical_clip_writer.go).
func (c *PostgresMediaCommitter) UpdateFolderPathForClip(ctx context.Context, assetID, folderPath string) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("UpdateFolderPathForClip: committer not wired")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var sourceVersion string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(source_version,'') FROM media_assets WHERE id=$1`, assetID).Scan(&sourceVersion); err != nil {
		return fmt.Errorf("UpdateFolderPathForClip: asset: %w", err)
	}
	if sourceVersion == "" {
		return fmt.Errorf("UpdateFolderPathForClip: source_version is empty for %s", assetID)
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	if err := c.UpdateFolderPathTx(ctx, tx, assetID, "", folderPath, updatedAt); err != nil {
		return err
	}
	if err := c.CommitIndexEventTx(ctx, tx, assetID, "youtube", sourceVersion, "video"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// Compile-time assertions: PostgresMediaCommitter is the canonical YouTube
// clip writer on the PostgreSQL media SSOT.
var (
	_ youtubeports.ClipAtomicWriter = (*PostgresMediaCommitter)(nil)
	_ localized.LocalizedClipWriter = (*PostgresMediaCommitter)(nil)
	_ texttracksTimedCueWriter      = (*PostgresMediaCommitter)(nil)
)

// texttracksTimedCueWriter mirrors texttracks.TimedCueWriter without
// importing the package here (the import would create no cycle, but the
// alias keeps the assertion shape identical to the SQLite side).
type texttracksTimedCueWriter interface {
	ReplaceTranscriptCues(ctx context.Context, assetID string, byLang map[string][]detail.TimedCue) error
}
