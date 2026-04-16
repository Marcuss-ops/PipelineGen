package catalogdb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// CatalogDB provides a normalized local SQLite catalog for clips coming from
// Artlist, Clip Drive, and Stock Drive.
type CatalogDB struct {
	db   *sql.DB
	path string
}

// Open opens or creates the local catalog database and initializes the schema.
func Open(path string) (*CatalogDB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create catalog dir: %w", err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite catalog: %w", err)
	}

	catalog := &CatalogDB{db: db, path: path}
	if err := catalog.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite catalog: %w", err)
	}

	return catalog, nil
}

func (c *CatalogDB) initSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS clips (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			source_id TEXT NOT NULL,
			provider TEXT,
			title TEXT,
			description TEXT,
			filename TEXT,
			category TEXT,
			folder_id TEXT,
			folder_path TEXT,
			drive_file_id TEXT,
			drive_url TEXT,
			external_path TEXT,
			local_path TEXT,
			tags_json TEXT,
			duration_sec INTEGER DEFAULT 0,
			width INTEGER DEFAULT 0,
			height INTEGER DEFAULT 0,
			mime_type TEXT,
			file_ext TEXT,
			file_size_bytes INTEGER DEFAULT 0,
			created_at DATETIME,
			modified_at DATETIME,
			last_synced_at DATETIME,
			is_active INTEGER DEFAULT 1,
			metadata_json TEXT,
			UNIQUE(source, source_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_catalog_clips_source ON clips(source);`,
		`CREATE INDEX IF NOT EXISTS idx_catalog_clips_folder ON clips(folder_id);`,
		`CREATE INDEX IF NOT EXISTS idx_catalog_clips_modified ON clips(modified_at);`,
		`CREATE TABLE IF NOT EXISTS sync_state (
			source TEXT PRIMARY KEY,
			cursor TEXT,
			last_full_scan_at DATETIME,
			last_incremental_at DATETIME,
			updated_at DATETIME NOT NULL
		);`,
	}

	for _, stmt := range stmts {
		if _, err := c.db.Exec(stmt); err != nil {
			return fmt.Errorf("init catalog schema: %w", err)
		}
	}
	return nil
}

// Close closes the underlying SQLite database.
func (c *CatalogDB) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

// UpsertClip inserts or updates a normalized clip entry.
func (c *CatalogDB) UpsertClip(clip Clip) error {
	return c.BulkUpsertClips([]Clip{clip})
}

// BulkUpsertClips inserts or updates many clips inside a transaction.
func (c *CatalogDB) BulkUpsertClips(clips []Clip) error {
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

	now := time.Now().UTC()
	for _, clip := range clips {
		if clip.ID == "" {
			clip.ID = fmt.Sprintf("%s:%s", clip.Source, clip.SourceID)
		}
		if clip.LastSyncedAt.IsZero() {
			clip.LastSyncedAt = now
		}
		if !clip.IsActive {
			clip.IsActive = true
		}

		tagsJSON, jerr := json.Marshal(normalizeTags(clip.Tags))
		if jerr != nil {
			return fmt.Errorf("marshal clip tags: %w", jerr)
		}

		if _, err = stmt.Exec(
			clip.ID,
			clip.Source,
			clip.SourceID,
			clip.Provider,
			clip.Title,
			clip.Description,
			clip.Filename,
			clip.Category,
			clip.FolderID,
			clip.FolderPath,
			clip.DriveFileID,
			clip.DriveURL,
			clip.ExternalPath,
			clip.LocalPath,
			string(tagsJSON),
			clip.DurationSec,
			clip.Width,
			clip.Height,
			clip.MimeType,
			clip.FileExt,
			clip.FileSizeBytes,
			nullableTime(clip.CreatedAt),
			nullableTime(clip.ModifiedAt),
			nullableTime(clip.LastSyncedAt),
			boolToInt(clip.IsActive),
			clip.MetadataJSON,
		); err != nil {
			return fmt.Errorf("upsert catalog clip %s: %w", clip.ID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit catalog tx: %w", err)
	}
	return nil
}

// MarkSourceMissing marks all clips from a source that were not seen in the
// latest sync pass as inactive.
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
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate source ids: %w", err)
	}

	for _, sourceID := range toDisable {
		if _, err := c.db.Exec(`UPDATE clips SET is_active = 0, last_synced_at = ? WHERE source = ? AND source_id = ?`, time.Now().UTC(), source, sourceID); err != nil {
			return fmt.Errorf("mark missing clip inactive: %w", err)
		}
	}
	return nil
}

// SearchClips performs a simple local search over the normalized catalog.
func (c *CatalogDB) SearchClips(opts SearchOptions) ([]SearchResult, error) {
	if c == nil || c.db == nil {
		return nil, nil
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}

	var args []interface{}
	var where []string
	if opts.OnlyActive || !opts.OnlyActive {
		where = append(where, "is_active = 1")
	}
	if opts.Source != "" {
		where = append(where, "source = ?")
		args = append(args, opts.Source)
	}
	if opts.FolderID != "" {
		where = append(where, "folder_id = ?")
		args = append(args, opts.FolderID)
	}
	if opts.MinDuration > 0 {
		where = append(where, "duration_sec >= ?")
		args = append(args, opts.MinDuration)
	}
	if opts.MaxDuration > 0 {
		where = append(where, "duration_sec <= ?")
		args = append(args, opts.MaxDuration)
	}

	queryTerms := normalizeTags(strings.Fields(opts.Query))
	for _, term := range queryTerms {
		where = append(where, `(LOWER(title) LIKE ? OR LOWER(description) LIKE ? OR LOWER(filename) LIKE ? OR LOWER(tags_json) LIKE ? OR LOWER(folder_path) LIKE ?)`)
		like := "%" + strings.ToLower(term) + "%"
		args = append(args, like, like, like, like, like)
	}

	stmt := `SELECT id, source, source_id, provider, title, description, filename, category, folder_id, folder_path, drive_file_id, drive_url, external_path, local_path, tags_json, duration_sec, width, height, mime_type, file_ext, file_size_bytes, created_at, modified_at, last_synced_at, is_active, metadata_json FROM clips`
	if len(where) > 0 {
		stmt += ` WHERE ` + strings.Join(where, ` AND `)
	}
	stmt += ` ORDER BY modified_at DESC, last_synced_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := c.db.Query(stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("search catalog: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		clip, err := scanClip(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, SearchResult{Clip: clip, Score: scoreClip(clip, queryTerms)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate catalog results: %w", err)
	}
	return results, nil
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

func scanClip(scanner interface{ Scan(dest ...interface{}) error }) (Clip, error) {
	var clip Clip
	var tagsJSON string
	var createdAt, modifiedAt, lastSyncedAt sql.NullTime
	var isActive int
	if err := scanner.Scan(
		&clip.ID,
		&clip.Source,
		&clip.SourceID,
		&clip.Provider,
		&clip.Title,
		&clip.Description,
		&clip.Filename,
		&clip.Category,
		&clip.FolderID,
		&clip.FolderPath,
		&clip.DriveFileID,
		&clip.DriveURL,
		&clip.ExternalPath,
		&clip.LocalPath,
		&tagsJSON,
		&clip.DurationSec,
		&clip.Width,
		&clip.Height,
		&clip.MimeType,
		&clip.FileExt,
		&clip.FileSizeBytes,
		&createdAt,
		&modifiedAt,
		&lastSyncedAt,
		&isActive,
		&clip.MetadataJSON,
	); err != nil {
		return Clip{}, fmt.Errorf("scan catalog clip: %w", err)
	}
	if createdAt.Valid {
		clip.CreatedAt = createdAt.Time
	}
	if modifiedAt.Valid {
		clip.ModifiedAt = modifiedAt.Time
	}
	if lastSyncedAt.Valid {
		clip.LastSyncedAt = lastSyncedAt.Time
	}
	clip.IsActive = isActive == 1
	_ = json.Unmarshal([]byte(tagsJSON), &clip.Tags)
	return clip, nil
}

func nullableTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func normalizeTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(strings.ToLower(tag))
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result
}

func scoreClip(clip Clip, queryTerms []string) float64 {
	if len(queryTerms) == 0 {
		return 1
	}
	text := strings.ToLower(strings.Join([]string{clip.Title, clip.Description, clip.Filename, strings.Join(clip.Tags, " "), clip.FolderPath}, " "))
	var score float64
	for _, term := range queryTerms {
		if strings.Contains(text, term) {
			score += 1
		}
	}
	if clip.Source == SourceClipDrive {
		score += 0.15
	} else if clip.Source == SourceArtlist {
		score += 0.1
	} else if clip.Source == SourceStockDrive {
		score += 0.05
	}
	return score
}
