package catalogdb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// CatalogDB provides a normalized local SQLite catalog for clips coming from
// Artlist, Clip Drive, and Stock Drive.
type CatalogDB struct {
	db       *sql.DB
	path     string
	ftsReady bool
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
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite catalog: %w", err)
	}
	if err := catalog.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
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

	if err := c.initFTS(); err != nil {
		fmt.Printf("WARN: FTS5 not available, using fallback text search: %v\n", err)
	}

	return nil
}

func (c *CatalogDB) initFTS() error {
	_, err := c.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS clips_fts USING fts5(
		id UNINDEXED,
		title,
		description,
		filename,
		category,
		folder_path,
		tags,
		metadata
	);`)
	if err == nil {
		c.ftsReady = true
	}
	return err
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

	ftsDelete, err := tx.Prepare(`DELETE FROM clips_fts WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("prepare fts delete: %w", err)
	}
	defer ftsDelete.Close()

	ftsInsert, err := tx.Prepare(`INSERT INTO clips_fts(id, title, description, filename, category, folder_path, tags, metadata) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare fts insert: %w", err)
	}
	defer ftsInsert.Close()

	now := time.Now().UTC()
	for _, clip := range clips {
		if clip.ID == "" {
			clip.ID = fmt.Sprintf("%s:%s", clip.Source, clip.SourceID)
		}
		if clip.LastSyncedAt.IsZero() {
			clip.LastSyncedAt = now
		}
		clip.IsActive = clip.IsActive || true

		normalizedTags := normalizeTags(clip.Tags)
		tagsJSON, jerr := json.Marshal(normalizedTags)
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

		if c.ftsReady {
			if _, err = ftsDelete.Exec(clip.ID); err != nil {
				return fmt.Errorf("delete existing fts row: %w", err)
			}
			if _, err = ftsInsert.Exec(clip.ID, clip.Title, clip.Description, clip.Filename, clip.Category, clip.FolderPath, strings.Join(normalizedTags, " "), clip.MetadataJSON); err != nil {
				return fmt.Errorf("insert fts row: %w", err)
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit catalog tx: %w", err)
	}
	return nil
}

// GetClip returns a single clip by normalized ID.
func (c *CatalogDB) GetClip(id string) (*Clip, error) {
	if c == nil || c.db == nil {
		return nil, nil
	}
	row := c.db.QueryRow(`SELECT id, source, source_id, provider, title, description, filename, category, folder_id, folder_path, drive_file_id, drive_url, external_path, local_path, tags_json, duration_sec, width, height, mime_type, file_ext, file_size_bytes, created_at, modified_at, last_synced_at, is_active, metadata_json FROM clips WHERE id = ?`, id)
	clip, err := scanClip(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &clip, nil
}

// GetStats returns basic aggregate information about the catalog.
func (c *CatalogDB) GetStats() (map[string]int, error) {
	stats := map[string]int{
		"total": 0,
	}
	if c == nil || c.db == nil {
		return stats, nil
	}
	rows, err := c.db.Query(`SELECT source, COUNT(*) FROM clips WHERE is_active = 1 GROUP BY source`)
	if err != nil {
		return nil, fmt.Errorf("catalog stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var source string
		var count int
		if err := rows.Scan(&source, &count); err != nil {
			return nil, err
		}
		stats[source] = count
		stats["total"] += count
	}
	return stats, rows.Err()
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

// SearchClips performs a ranked local search over the normalized catalog.
func (c *CatalogDB) SearchClips(opts SearchOptions) ([]SearchResult, error) {
	if c == nil || c.db == nil {
		return nil, nil
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	terms := normalizeTags(strings.Fields(opts.Query))

	if c.ftsReady {
		results, err := c.searchViaFTS(opts, terms, limit)
		if err == nil && len(results) > 0 {
			sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
			return trimResults(results, limit), nil
		}
	}
	return c.searchViaLike(opts, terms, limit)
}

func (c *CatalogDB) searchViaFTS(opts SearchOptions, terms []string, limit int) ([]SearchResult, error) {
	if !c.ftsReady || len(terms) == 0 {
		return nil, nil
	}
	matchExpr := strings.Join(terms, " OR ")
	query := `
	SELECT c.id, c.source, c.source_id, c.provider, c.title, c.description, c.filename, c.category, c.folder_id, c.folder_path, c.drive_file_id, c.drive_url, c.external_path, c.local_path, c.tags_json, c.duration_sec, c.width, c.height, c.mime_type, c.file_ext, c.file_size_bytes, c.created_at, c.modified_at, c.last_synced_at, c.is_active, c.metadata_json, bm25(clips_fts) as rank
	FROM clips_fts
	JOIN clips c ON c.id = clips_fts.id
	WHERE clips_fts MATCH ? AND c.is_active = 1`
	var args []interface{}
	args = append(args, matchExpr)
	if opts.Source != "" {
		query += ` AND c.source = ?`
		args = append(args, opts.Source)
	}
	if opts.FolderID != "" {
		query += ` AND c.folder_id = ?`
		args = append(args, opts.FolderID)
	}
	if opts.MinDuration > 0 {
		query += ` AND c.duration_sec >= ?`
		args = append(args, opts.MinDuration)
	}
	if opts.MaxDuration > 0 {
		query += ` AND c.duration_sec <= ?`
		args = append(args, opts.MaxDuration)
	}
	query += ` ORDER BY rank LIMIT ?`
	args = append(args, limit)

	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		clip, rank, err := scanClipWithRank(rows)
		if err != nil {
			return nil, err
		}
		score := scoreClip(clip, terms) + normalizeFTSScore(rank)
		results = append(results, SearchResult{Clip: clip, Score: score})
	}
	return results, rows.Err()
}

func (c *CatalogDB) searchViaLike(opts SearchOptions, terms []string, limit int) ([]SearchResult, error) {
	var args []interface{}
	var where []string
	where = append(where, "is_active = 1")
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
	for _, term := range terms {
		where = append(where, `(LOWER(title) LIKE ? OR LOWER(description) LIKE ? OR LOWER(filename) LIKE ? OR LOWER(tags_json) LIKE ? OR LOWER(folder_path) LIKE ? OR LOWER(metadata_json) LIKE ?)`)
		like := "%" + strings.ToLower(term) + "%"
		args = append(args, like, like, like, like, like, like)
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
		results = append(results, SearchResult{Clip: clip, Score: scoreClip(clip, terms)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate catalog results: %w", err)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return trimResults(results, limit), nil
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

// GetSyncState returns the stored sync state for a source.
func (c *CatalogDB) GetSyncState(source string) (*SyncState, error) {
	if c == nil || c.db == nil || source == "" {
		return nil, nil
	}
	var state SyncState
	var lastFull, lastIncremental sql.NullTime
	row := c.db.QueryRow(`SELECT source, cursor, last_full_scan_at, last_incremental_at, updated_at FROM sync_state WHERE source = ?`, source)
	if err := row.Scan(&state.Source, &state.Cursor, &lastFull, &lastIncremental, &state.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get sync state: %w", err)
	}
	if lastFull.Valid {
		state.LastFullScanAt = lastFull.Time
	}
	if lastIncremental.Valid {
		state.LastIncrementalAt = lastIncremental.Time
	}
	return &state, nil
}

func scanClip(scanner interface {
	Scan(dest ...interface{}) error
}) (Clip, error) {
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

func scanClipWithRank(scanner interface {
	Scan(dest ...interface{}) error
}) (Clip, float64, error) {
	var clip Clip
	var tagsJSON string
	var createdAt, modifiedAt, lastSyncedAt sql.NullTime
	var isActive int
	var rank float64
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
		&rank,
	); err != nil {
		return Clip{}, 0, fmt.Errorf("scan catalog clip with rank: %w", err)
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
	return clip, rank, nil
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
		return sourcePriority(clip.Source)
	}
	text := strings.ToLower(strings.Join([]string{clip.Title, clip.Description, clip.Filename, strings.Join(clip.Tags, " "), clip.FolderPath, clip.MetadataJSON}, " "))
	var score float64
	for _, term := range queryTerms {
		if strings.Contains(text, term) {
			score += 1
		}
		for _, tag := range clip.Tags {
			if term == tag {
				score += 0.35
			}
		}
	}
	if clip.DurationSec >= 3 && clip.DurationSec <= 20 {
		score += 0.2
	}
	score += sourcePriority(clip.Source)
	return score
}

func sourcePriority(source string) float64 {
	switch source {
	case SourceClipDrive:
		return 0.25
	case SourceArtlist:
		return 0.18
	case SourceStockDrive:
		return 0.12
	default:
		return 0
	}
}

func normalizeFTSScore(rank float64) float64 {
	if rank >= 0 {
		return 0.1
	}
	return -rank
}

func trimResults(results []SearchResult, limit int) []SearchResult {
	if len(results) <= limit {
		return results
	}
	return results[:limit]
}
