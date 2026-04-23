package catalogdb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// UpsertClip inserts or updates a normalized clip entry.
func (c *CatalogDB) UpsertClip(clip Clip) error {
	return c.BulkUpsertClips([]Clip{clip})
}

// BulkUpsertClips inserts or updates many clips inside a transaction.
func (c *CatalogDB) BulkUpsertClips(clips []Clip) (err error) {
	if c == nil || c.db == nil || len(clips) == 0 {
		return nil
	}

	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("begin catalog tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.Prepare(`
		INSERT INTO clips (
			id, source, source_id, provider, title, description, filename, category,
			folder_id, folder_path, drive_file_id, drive_url, external_path, local_path,
			tags_json, duration_sec, width, height, mime_type, file_ext,
			file_size_bytes, created_at, modified_at, last_synced_at, is_active, metadata_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source=excluded.source,
			source_id=excluded.source_id,
			provider=excluded.provider,
			title=excluded.title,
			description=excluded.description,
			filename=excluded.filename,
			category=excluded.category,
			folder_id=excluded.folder_id,
			folder_path=excluded.folder_path,
			drive_file_id=excluded.drive_file_id,
			drive_url=excluded.drive_url,
			external_path=excluded.external_path,
			local_path=excluded.local_path,
			tags_json=excluded.tags_json,
			duration_sec=excluded.duration_sec,
			width=excluded.width,
			height=excluded.height,
			mime_type=excluded.mime_type,
			file_ext=excluded.file_ext,
			file_size_bytes=excluded.file_size_bytes,
			created_at=excluded.created_at,
			modified_at=excluded.modified_at,
			last_synced_at=excluded.last_synced_at,
			is_active=excluded.is_active,
			metadata_json=excluded.metadata_json;
	`)
	if err != nil {
		return fmt.Errorf("prepare catalog upsert: %w", err)
	}
	defer stmt.Close()

	var ftsDelete, ftsInsert *sql.Stmt
	if c.ftsReady {
		ftsDelete, _ = tx.Prepare(`DELETE FROM clips_fts WHERE id = ?`)
		ftsInsert, _ = tx.Prepare(`INSERT INTO clips_fts(id, title, description, filename, category, folder_path, tags, metadata) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	}
	if ftsDelete != nil {
		defer ftsDelete.Close()
	}
	if ftsInsert != nil {
		defer ftsInsert.Close()
	}

	now := time.Now().UTC()
	for _, clip := range clips {
		if clip.ID == "" {
			clip.ID = fmt.Sprintf("%s:%s", clip.Source, clip.SourceID)
		}
		if clip.LastSyncedAt.IsZero() {
			clip.LastSyncedAt = now
		}
		clip.IsActive = true

		normalizedTags := normalizeTags(clip.Tags)
		tagsJSON, _ := json.Marshal(normalizedTags)

		if _, err = stmt.Exec(
			clip.ID, clip.Source, clip.SourceID, clip.Provider, clip.Title,
			clip.Description, clip.Filename, clip.Category, clip.FolderID,
			clip.FolderPath, clip.DriveFileID, clip.DriveURL, clip.ExternalPath,
			clip.LocalPath, string(tagsJSON), clip.DurationSec, clip.Width,
			clip.Height, clip.MimeType, clip.FileExt, clip.FileSizeBytes,
			nullableTime(clip.CreatedAt), nullableTime(clip.ModifiedAt),
			nullableTime(clip.LastSyncedAt), boolToInt(clip.IsActive), clip.MetadataJSON,
		); err != nil {
			return fmt.Errorf("upsert catalog clip %s: %w", clip.ID, err)
		}

		if c.ftsReady && ftsDelete != nil && ftsInsert != nil {
			_, _ = ftsDelete.Exec(clip.ID)
			_, _ = ftsInsert.Exec(clip.ID, clip.Title, clip.Description, clip.Filename, clip.Category, clip.FolderPath, strings.Join(normalizedTags, " "), clip.MetadataJSON)
		}
	}

	return tx.Commit()
}

// MarkSourceMissing marks all clips from a source that were not seen in the latest sync pass as inactive.
func (c *CatalogDB) MarkSourceMissing(source string, activeSourceIDs []string) error {
	if c == nil || c.db == nil || source == "" {
		return nil
	}

	active := make(map[string]struct{}, len(activeSourceIDs))
	for _, id := range activeSourceIDs {
		active[id] = struct{}{}
	}

	rows, err := c.db.Query(`SELECT source_id FROM clips WHERE source = ?`, source)
	if err != nil {
		return fmt.Errorf("query source ids: %w", err)
	}
	defer rows.Close()

	var toDisable []string
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			return fmt.Errorf("scan source id: %w", err)
		}
		if _, ok := active[sourceID]; !ok {
			toDisable = append(toDisable, sourceID)
		}
	}

	for _, sourceID := range toDisable {
		if _, err := c.db.Exec(`UPDATE clips SET is_active = 0, last_synced_at = ? WHERE source = ? AND source_id = ?`, time.Now().UTC(), source, sourceID); err != nil {
			return fmt.Errorf("mark missing clip inactive: %w", err)
		}
	}
	return nil
}

// UpsertSyncState persists the sync cursor and timestamps for a given source.
func (c *CatalogDB) UpsertSyncState(state SyncState) error {
	if c == nil || c.db == nil {
		return nil
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	_, err := c.db.Exec(`
		INSERT INTO sync_state (source, cursor, last_full_scan_at, last_incremental_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(source) DO UPDATE SET
			cursor=excluded.cursor,
			last_full_scan_at=excluded.last_full_scan_at,
			last_incremental_at=excluded.last_incremental_at,
			updated_at=excluded.updated_at
	`, state.Source, state.Cursor, nullableTime(state.LastFullScanAt), nullableTime(state.LastIncrementalAt), state.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert sync state: %w", err)
	}
	return nil
}
