package catalogdb

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

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
	stats := map[string]int{"total": 0}
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
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return trimResults(results, limit), nil
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
