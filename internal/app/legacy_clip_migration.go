package app

import (
	"database/sql"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/storage"
)

func runLegacyClipMigrations(dbs *databases, log *zap.Logger) error {
	if dbs == nil {
		return nil
	}

	if err := migrateLegacyClipDB(dbs.stock, "stock", log); err != nil {
		return fmt.Errorf("failed to migrate stock clips db: %w", err)
	}
	if err := migrateLegacyClipDB(dbs.artlist, "artlist", log); err != nil {
		return fmt.Errorf("failed to migrate artlist clips db: %w", err)
	}
	return nil
}

func migrateLegacyClipDB(db *storage.SQLiteDB, source string, log *zap.Logger) error {
	if db == nil || db.DB == nil {
		return nil
	}

	exists, err := tableExists(db.DB, "clips")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	if err := ensureMediaAssetsTable(db.DB); err != nil {
		return err
	}

	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	importSQL, args := legacyClipMigrationSQL(source)
	_, err = tx.Exec(importSQL, args...)
	if err != nil {
		return err
	}

	if _, err := tx.Exec("DROP TABLE clips"); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	log.Info("migrated legacy clips DB to media_assets", zap.String("source", source))
	return nil
}

func legacyClipMigrationSQL(source string) (string, []interface{}) {
	baseInsert := `
		INSERT OR REPLACE INTO media_assets (
			id, source, name, tags, tags_norm, embedding_json, duration_ms, url, created_at, metadata_json
		)
		SELECT
			id,
			COALESCE(source, ?),
			COALESCE(name, ''),
			COALESCE(tags, '[]'),
			LOWER(COALESCE(name, '')),
			COALESCE(embedding_json, '[]'),
			COALESCE(duration, 0) * 1000,
			COALESCE(external_url, COALESCE(drive_link, COALESCE(download_link, ''))),
			COALESCE(created_at, datetime('now')),
			json_object(
				'filename', COALESCE(filename, ''),
				'folder_id', COALESCE(folder_id, ''),
				'parent_folder_id', COALESCE(parent_folder_id, ''),
				'folder_path', COALESCE(folder_path, ''),
				'group_name', COALESCE(group_name, ''),
				'media_type', COALESCE(media_type, ?),
				'drive_link', COALESCE(drive_link, ''),
				'download_link', COALESCE(download_link, ''),
				'drive_file_id', COALESCE(drive_file_id, ''),
				'file_hash', COALESCE(file_hash, ''),
				'local_path', COALESCE(local_path, ''),
				'category', COALESCE(category, ''),
				'status', COALESCE(status, ''),
				'error', COALESCE(error, ''),
				'thumb_url', COALESCE(thumb_url, ''),
				'phash', COALESCE(phash, ''),
				'visual_embedding_json', COALESCE(visual_embedding_json, '[]'),
				'search_text', COALESCE(search_text, ''),
				'deleted_at', deleted_at
			)
		FROM clips
	`
	args := []interface{}{source, source}

	if source == "artlist" {
		baseInsert = `
		INSERT OR REPLACE INTO media_assets (
			id, source, name, tags, tags_norm, embedding_json, duration_ms, url, created_at, metadata_json
		)
		SELECT
			id,
			COALESCE(source, ?),
			COALESCE(name, ''),
			COALESCE(tags, '[]'),
			LOWER(COALESCE(name, '')),
			COALESCE(embedding_json, '[]'),
			COALESCE(duration, 0) * 1000,
			COALESCE(external_url, COALESCE(drive_link, COALESCE(download_link, ''))),
			COALESCE(created_at, datetime('now')),
			json_object(
				'filename', COALESCE(filename, ''),
				'folder_id', COALESCE(folder_id, ''),
				'parent_folder_id', COALESCE(parent_folder_id, ''),
				'folder_path', COALESCE(folder_path, ''),
				'group_name', COALESCE(group_name, ''),
				'media_type', COALESCE(media_type, ?),
				'drive_link', COALESCE(drive_link, ''),
				'download_link', COALESCE(download_link, ''),
				'drive_file_id', COALESCE(drive_file_id, ''),
				'file_hash', COALESCE(file_hash, ''),
				'local_path', COALESCE(local_path, ''),
				'category', COALESCE(category, ''),
				'status', COALESCE(status, ''),
				'error', COALESCE(error, ''),
				'thumb_url', COALESCE(thumb_url, ''),
				'phash', COALESCE(phash, ''),
				'visual_embedding_json', COALESCE(visual_embedding_json, '[]'),
				'search_text', COALESCE(search_text, ''),
				'scene_type', COALESCE(scene_type, ''),
				'usable_for_json', COALESCE(usable_for_json, '[]'),
				'avoid_for_json', COALESCE(avoid_for_json, '[]'),
				'quality_score', COALESCE(quality_score, 0),
				'reuse_count', COALESCE(reuse_count, 0),
				'last_used_at', COALESCE(last_used_at, ''),
				'last_indexed_at', COALESCE(last_indexed_at, ''),
				'deleted_at', deleted_at
			)
		FROM clips
	`
		args = []interface{}{source, source}
	}

	return baseInsert, args
}

func ensureMediaAssetsTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '[]',
			tags_norm TEXT NOT NULL DEFAULT '',
			embedding_json TEXT NOT NULL DEFAULT '[]',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			url TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			metadata_json TEXT NOT NULL DEFAULT '{}'
		)
	`)
	if err != nil {
		return err
	}

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_media_source ON media_assets(source)`,
		`CREATE INDEX IF NOT EXISTS idx_media_tags ON media_assets(tags_norm)`,
	}
	for _, stmt := range indexes {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}

	return nil
}

func tableExists(db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=? LIMIT 1`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(name) == table, nil
}
