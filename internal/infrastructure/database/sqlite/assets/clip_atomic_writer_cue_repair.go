package assets

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

func (w *ClipAtomicWriterAdapter) UpdateFolderPath(ctx context.Context, assetID, folderPath string) error {
	if w == nil || w.db == nil || w.box == nil {
		return fmt.Errorf("UpdateFolderPath: adapter not wired")
	}
	tx, err := w.db.BeginTx(ctx, nil)
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
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(source_version,'') FROM media_assets WHERE id=?`, assetID).Scan(&sourceVersion); err != nil {
		return fmt.Errorf("UpdateFolderPath: asset: %w", err)
	}
	if sourceVersion == "" {
		return fmt.Errorf("UpdateFolderPath: source_version is empty for %s", assetID)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE media_assets SET folder_path=?,updated_at=? WHERE id=?`, folderPath, time.Now().UTC().Format(time.RFC3339), assetID); err != nil {
		return err
	}
	key, payload, err := outboxevents.BuildReindexEnvelopeV1(assetID, outboxevents.ReindexEnvelopeV1Schema, sourceVersion, time.Now().UTC())
	if err != nil {
		return err
	}
	if _, err := w.box.Enqueue(ctx, tx, outboxevents.EventAssetIndexRequested, assetID, "media_asset", payload, key); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (w *ClipAtomicWriterAdapter) ReplaceTranscriptCues(ctx context.Context, assetID string, byLang map[string][]asset.TimedCue) error {
	if w == nil || w.db == nil {
		return fmt.Errorf("ReplaceTranscriptCues: adapter not wired")
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	rows, err := tx.QueryContext(ctx, `SELECT id,language_code FROM asset_text_tracks WHERE asset_id=? AND text_kind='transcript' AND status='READY' AND is_current=1`, assetID)
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
	if _, err = tx.ExecContext(ctx, `DELETE FROM asset_text_track_segments WHERE track_id IN (SELECT id FROM asset_text_tracks WHERE asset_id=? AND text_kind='transcript' AND status='READY' AND is_current=1)`, assetID); err != nil {
		return err
	}
	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO asset_text_track_segments(track_id,sequence_no,start_ms,end_ms,text) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, lang := range langs {
		id, ok := ids[lang]
		if !ok {
			return fmt.Errorf("ReplaceTranscriptCues: missing READY transcript %s", lang)
		}
		cues := append([]asset.TimedCue(nil), byLang[lang]...)
		sort.SliceStable(cues, func(i, j int) bool { return cues[i].StartMs < cues[j].StartMs })
		for i, c := range cues {
			if c.StartMs < 0 || c.EndMs <= c.StartMs {
				return fmt.Errorf("ReplaceTranscriptCues: invalid cue %s #%d", lang, i+1)
			}
			if _, err := stmt.ExecContext(ctx, id, i+1, c.StartMs, c.EndMs, c.Text); err != nil {
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
