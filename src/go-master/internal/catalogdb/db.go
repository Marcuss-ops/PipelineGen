package catalogdb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open opens or creates the local catalog database and initializes the schema.
func Open(path string) (*CatalogDB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create catalog dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
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
