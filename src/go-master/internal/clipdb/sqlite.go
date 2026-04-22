package clipdb

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

type SQLiteDB struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLiteDB, error) {
	// Usiamo il driver "sqlite" (modernc.org) che supporta FTS5 out-of-the-box in Go
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	// 1. Ottimizzazioni di Concorrenza (WAL Mode)
	_, err = db.Exec(`
		PRAGMA journal_mode=WAL;
		PRAGMA synchronous=NORMAL;
		PRAGMA busy_timeout=5000;
		PRAGMA foreign_keys=ON;
	`)
	if err != nil {
		return nil, fmt.Errorf("errore pragma: %v", err)
	}

	// 2. Creazione Schema con Health Check e Metadata
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS clips (
			id TEXT PRIMARY KEY,
			title TEXT,
			source TEXT,
			url TEXT NOT NULL,
			duration REAL NOT NULL CHECK(duration >= 0),
			width INTEGER DEFAULT 1920,
			height INTEGER DEFAULT 1080,
			tags TEXT,
			http_status INTEGER DEFAULT 0,
			last_checked DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		-- Tabella FTS5 per ricerca semantica veloce (Full Text Search)
		-- Usiamo il trigger per tenerla sincronizzata
		CREATE VIRTUAL TABLE IF NOT EXISTS clips_fts USING fts5(
			id UNINDEXED,
			title,
			tags,
			content='clips',
			content_rowid='rowid'
		);

		-- Trigger per sincronizzazione FTS5
		CREATE TRIGGER IF NOT EXISTS clips_ai AFTER INSERT ON clips BEGIN
			INSERT INTO clips_fts(rowid, id, title, tags) VALUES (new.rowid, new.id, new.title, new.tags);
		END;
		CREATE TRIGGER IF NOT EXISTS clips_ad AFTER DELETE ON clips BEGIN
			INSERT INTO clips_fts(clips_fts, rowid, id, title, tags) VALUES('delete', old.rowid, old.id, old.title, old.tags);
		END;
		CREATE TRIGGER IF NOT EXISTS clips_au AFTER UPDATE ON clips BEGIN
			INSERT INTO clips_fts(clips_fts, rowid, id, title, tags) VALUES('delete', old.rowid, old.id, old.title, old.tags);
			INSERT INTO clips_fts(rowid, id, title, tags) VALUES (new.rowid, new.id, new.title, new.tags);
		END;
	`)
	if err != nil {
		return nil, fmt.Errorf("errore creazione schema: %v", err)
	}

	return &SQLiteDB{db: db}, nil
}

func (s *SQLiteDB) RawDB() *sql.DB {
	return s.db
}

func (s *SQLiteDB) UpsertClip(c *ClipEntry) error {
	tagsStr := strings.Join(c.Tags, " ")
	// Upsert Idempotente: aggiorna URL, Durata e Tags, mantiene il resto
	_, err := s.db.Exec(`
		INSERT INTO clips (id, title, source, url, duration, tags, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			url = excluded.url,
			duration = excluded.duration,
			tags = CASE 
				WHEN clips.tags LIKE '%' || excluded.tags || '%' THEN clips.tags 
				ELSE clips.tags || ' ' || excluded.tags 
			END,
			updated_at = CURRENT_TIMESTAMP
	`, c.ClipID, c.Filename, c.Source, c.LocalPath, c.Duration, tagsStr)
	return err
}

func (s *SQLiteDB) UpdateHealth(id string, status int) error {
	_, err := s.db.Exec(`
		UPDATE clips SET http_status = ?, last_checked = CURRENT_TIMESTAMP WHERE id = ?
	`, status, id)
	return err
}

type QueryParams struct {
	Tags     []string
	MinDur   float64
	MaxDur   float64
	Limit    int
}

func (s *SQLiteDB) Resolve(params QueryParams) ([]*ClipEntry, error) {
	if params.Limit <= 0 {
		params.Limit = 10
	}

	// Filtro chirurgico: restituisce solo link non controllati (0) o verificati (200)
	sqlQuery := `
		SELECT c.id, c.title, c.source, c.url, c.duration, c.tags 
		FROM clips c
		JOIN clips_fts f ON c.rowid = f.rowid
		WHERE (c.http_status = 200 OR c.http_status = 0)
	`
	var args []interface{}

	if len(params.Tags) > 0 {
		// Sintassi FTS5 per ricerca multipla
		ftsQuery := strings.Join(params.Tags, " OR ")
		sqlQuery += " AND clips_fts MATCH ?"
		args = append(args, ftsQuery)
	}

	if params.MinDur > 0 {
		sqlQuery += " AND c.duration >= ?"
		args = append(args, params.MinDur)
	}

	sqlQuery += " ORDER BY c.updated_at DESC LIMIT ?"
	args = append(args, params.Limit)

	rows, err := s.db.Query(sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*ClipEntry
	for rows.Next() {
		var c ClipEntry
		var tagsStr string
		if err := rows.Scan(&c.ClipID, &c.Filename, &c.Source, &c.LocalPath, &c.Duration, &tagsStr); err != nil {
			return nil, err
		}
		c.Tags = strings.Fields(tagsStr)
		results = append(results, &c)
	}

	return results, nil
}

func (s *SQLiteDB) Close() error {
	return s.db.Close()
}
