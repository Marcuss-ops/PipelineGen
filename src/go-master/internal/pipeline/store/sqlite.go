package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// PipelineStore gestisce lo stato persistente della pipeline (Coda, Video, Cache AI)
type PipelineStore struct {
	db *sql.DB
}

// NewPipelineStore apre un nuovo database SQLite in modalit?? WAL per concorrenza ottimale
func NewPipelineStore(path string) (*PipelineStore, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	s := &PipelineStore{db: db}
	if err := s.initSchema(); err != nil {
		return nil, err
	}

	return s, nil
}

// initSchema crea le tabelle necessarie se non esistono
func (s *PipelineStore) initSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS processed_videos (
			video_id TEXT PRIMARY KEY,
			title TEXT,
			channel TEXT,
			category TEXT,
			protagonist TEXT,
			transcript_hash TEXT,
			discovered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			status TEXT DEFAULT "discovered"
		)`,
		`CREATE TABLE IF NOT EXISTS pipeline_queue (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			video_id TEXT UNIQUE,
			priority INTEGER DEFAULT 0,
			state TEXT DEFAULT "pending",
			attempts INTEGER DEFAULT 0,
			last_error TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			locked_until DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS ai_cache (
			hash TEXT PRIMARY KEY,
			prompt_type TEXT,
			response_json TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS clips (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			video_id TEXT,
			drive_id TEXT,
			folder_path TEXT,
			start_sec INTEGER,
			duration INTEGER,
			reason TEXT,
			uploaded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(video_id) REFERENCES processed_videos(video_id)
		)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("schema error: %w", err)
		}
	}
	return nil
}

// AddToQueue aggiunge un video alla coda persistente (atomico)
func (s *PipelineStore) AddToQueue(ctx context.Context, videoID string) error {
	_, err := s.db.ExecContext(ctx, 
		"INSERT OR IGNORE INTO pipeline_queue (video_id) VALUES (?)", 
		videoID)
	return err
}

// PopNextJob preleva il prossimo job libero dalla coda (lock atomico)
func (s *PipelineStore) PopNextJob(ctx context.Context, leaseDuration time.Duration) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var videoID string
	err = tx.QueryRowContext(ctx, `
		SELECT video_id FROM pipeline_queue 
		WHERE state = "pending" OR (state = "processing" AND locked_until < ?)
		ORDER BY priority DESC, created_at ASC LIMIT 1`, 
		time.Now().UTC()).Scan(&videoID)

	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	lockedUntil := time.Now().UTC().Add(leaseDuration)
	_, err = tx.ExecContext(ctx, 
		"UPDATE pipeline_queue SET state = \"processing\", locked_until = ?, attempts = attempts + 1, updated_at = ? WHERE video_id = ?", 
		lockedUntil, time.Now().UTC(), videoID)
	
	if err != nil {
		return "", err
	}

	return videoID, tx.Commit()
}

// Close chiude la connessione al database
func (s *PipelineStore) Close() error {
	return s.db.Close()
}
