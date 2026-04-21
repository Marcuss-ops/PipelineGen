package channelmonitor

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// ClipRunStore persists clip attempts for idempotence and recovery.
type ClipRunStore struct {
	path string
	db   *sql.DB
	mu   sync.RWMutex
}

// ClipRunDatabase is kept only for legacy JSON migration.
type ClipRunDatabase struct {
	LastSynced time.Time       `json:"last_synced"`
	Runs       []ClipRunRecord `json:"runs"`
}

// OpenClipRunStore opens or creates the local SQLite clip run DB.
func OpenClipRunStore(path string) (*ClipRunStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("empty clip run db path")
	}

	sqlitePath := normalizeClipRunDBPath(path)
	if err := os.MkdirAll(filepath.Dir(sqlitePath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create clip run db directory: %w", err)
	}

	db, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open clip run sqlite db: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to configure clip run sqlite db: %w", err)
	}

	store := &ClipRunStore{
		path: sqlitePath,
		db:   db,
	}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrateLegacyJSON(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func normalizeClipRunDBPath(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return strings.TrimSuffix(path, ".json") + ".sqlite"
	}
	return path
}

func (s *ClipRunStore) initSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS clip_runs (
			run_key TEXT PRIMARY KEY,
			video_id TEXT NOT NULL,
			title TEXT NOT NULL,
			folder_path TEXT,
			category TEXT,
			confidence REAL NOT NULL DEFAULT 0,
			needs_review INTEGER NOT NULL DEFAULT 0,
			segment_idx INTEGER NOT NULL DEFAULT 0,
			start_sec INTEGER NOT NULL,
			end_sec INTEGER NOT NULL,
			duration INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			file_name TEXT,
			drive_file_id TEXT,
			drive_file_url TEXT,
			txt_file_id TEXT,
			error TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_clip_runs_video ON clip_runs(video_id, start_sec, end_sec)`,
		`CREATE INDEX IF NOT EXISTS idx_clip_runs_status ON clip_runs(status)`,
		`CREATE INDEX IF NOT EXISTS idx_clip_runs_attention ON clip_runs(needs_review, status)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("clip run schema error: %w", err)
		}
	}
	return nil
}

func (s *ClipRunStore) migrateLegacyJSON() error {
	legacyPath := strings.TrimSuffix(s.path, ".sqlite") + ".json"
	if _, err := os.Stat(legacyPath); err != nil {
		return nil
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM clip_runs`).Scan(&count); err != nil {
		return fmt.Errorf("count clip runs before legacy migration: %w", err)
	}
	if count > 0 {
		return nil
	}

	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return fmt.Errorf("read legacy clip run db: %w", err)
	}

	var legacy ClipRunDatabase
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("parse legacy clip run db: %w", err)
	}

	for _, rec := range legacy.Runs {
		if rec.RunKey == "" {
			rec.RunKey = clipRunKey(rec.VideoID, rec.StartSec, rec.EndSec)
		}
		if err := s.Upsert(rec); err != nil {
			return fmt.Errorf("migrate legacy clip run %s: %w", rec.RunKey, err)
		}
	}

	if err := os.Rename(legacyPath, legacyPath+".bak"); err != nil {
		return fmt.Errorf("backup legacy clip run db: %w", err)
	}
	return nil
}

func clipRunKey(videoID string, startSec, endSec int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", videoID, startSec, endSec)))
	return fmt.Sprintf("%x", sum[:])
}

func clipRunNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func parseClipRunTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	return time.Time{}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func intToBool(v int) bool {
	return v != 0
}

func scanClipRunRow(scanner interface {
	Scan(dest ...any) error
}) (*ClipRunRecord, error) {
	var rec ClipRunRecord
	var needsReview int
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&rec.RunKey,
		&rec.VideoID,
		&rec.Title,
		&rec.FolderPath,
		&rec.Category,
		&rec.Confidence,
		&needsReview,
		&rec.SegmentIdx,
		&rec.StartSec,
		&rec.EndSec,
		&rec.Duration,
		&rec.Status,
		&rec.FileName,
		&rec.DriveFileID,
		&rec.DriveFileURL,
		&rec.TxtFileID,
		&rec.Error,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	rec.NeedsReview = intToBool(needsReview)
	rec.CreatedAt = parseClipRunTime(createdAt)
	rec.UpdatedAt = parseClipRunTime(updatedAt)
	return &rec, nil
}

func (s *ClipRunStore) upsertTx(tx *sql.Tx, record ClipRunRecord) error {
	now := clipRunNow()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = parseClipRunTime(now)
	}
	record.UpdatedAt = time.Now().UTC()

	var existingCreatedAt string
	err := tx.QueryRow(`SELECT created_at FROM clip_runs WHERE run_key = ?`, record.RunKey).Scan(&existingCreatedAt)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if existingCreatedAt != "" {
		record.CreatedAt = parseClipRunTime(existingCreatedAt)
	}

	_, err = tx.Exec(
		`INSERT INTO clip_runs (
			run_key, video_id, title, folder_path, category, confidence, needs_review,
			segment_idx, start_sec, end_sec, duration, status, file_name, drive_file_id,
			drive_file_url, txt_file_id, error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_key) DO UPDATE SET
			video_id=excluded.video_id,
			title=excluded.title,
			folder_path=excluded.folder_path,
			category=excluded.category,
			confidence=excluded.confidence,
			needs_review=excluded.needs_review,
			segment_idx=excluded.segment_idx,
			start_sec=excluded.start_sec,
			end_sec=excluded.end_sec,
			duration=excluded.duration,
			status=excluded.status,
			file_name=excluded.file_name,
			drive_file_id=excluded.drive_file_id,
			drive_file_url=excluded.drive_file_url,
			txt_file_id=excluded.txt_file_id,
			error=excluded.error,
			updated_at=excluded.updated_at`,
		record.RunKey,
		record.VideoID,
		record.Title,
		record.FolderPath,
		record.Category,
		record.Confidence,
		boolToInt(record.NeedsReview),
		record.SegmentIdx,
		record.StartSec,
		record.EndSec,
		record.Duration,
		record.Status,
		record.FileName,
		record.DriveFileID,
		record.DriveFileURL,
		record.TxtFileID,
		record.Error,
		record.CreatedAt.Format(time.RFC3339Nano),
		record.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// Get returns the clip run record for a given run key.
func (s *ClipRunStore) Get(runKey string) (*ClipRunRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(
		`SELECT run_key, video_id, title, folder_path, category, confidence, needs_review,
			segment_idx, start_sec, end_sec, duration, status, file_name, drive_file_id,
			drive_file_url, txt_file_id, error, created_at, updated_at
		 FROM clip_runs WHERE run_key = ?`,
		runKey,
	)
	rec, err := scanClipRunRow(row)
	if err != nil {
		return nil, false
	}
	return rec, true
}

// ListByVideo returns all clip runs for a video ordered by timestamp.
func (s *ClipRunStore) ListByVideo(videoID string) []ClipRunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT run_key, video_id, title, folder_path, category, confidence, needs_review,
			segment_idx, start_sec, end_sec, duration, status, file_name, drive_file_id,
			drive_file_url, txt_file_id, error, created_at, updated_at
		 FROM clip_runs WHERE video_id = ? ORDER BY start_sec ASC, segment_idx ASC`,
		videoID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []ClipRunRecord
	for rows.Next() {
		rec, err := scanClipRunRow(rows)
		if err != nil {
			continue
		}
		out = append(out, *rec)
	}
	return out
}

// ListAttentionNeeded returns all runs that failed or need review.
func (s *ClipRunStore) ListAttentionNeeded() []ClipRunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT run_key, video_id, title, folder_path, category, confidence, needs_review,
			segment_idx, start_sec, end_sec, duration, status, file_name, drive_file_id,
			drive_file_url, txt_file_id, error, created_at, updated_at
		 FROM clip_runs
		 WHERE needs_review = 1 OR status IN (?, ?)
		 ORDER BY updated_at DESC`,
		ClipRunStatusFailed,
		ClipRunStatusNeedsReview,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []ClipRunRecord
	for rows.Next() {
		rec, err := scanClipRunRow(rows)
		if err != nil {
			continue
		}
		out = append(out, *rec)
	}
	return out
}

// ListAll returns every persisted clip run.
func (s *ClipRunStore) ListAll() []ClipRunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT run_key, video_id, title, folder_path, category, confidence, needs_review,
			segment_idx, start_sec, end_sec, duration, status, file_name, drive_file_id,
			drive_file_url, txt_file_id, error, created_at, updated_at
		 FROM clip_runs
		 ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []ClipRunRecord
	for rows.Next() {
		rec, err := scanClipRunRow(rows)
		if err != nil {
			continue
		}
		out = append(out, *rec)
	}
	return out
}

// Upsert inserts or updates a clip run.
func (s *ClipRunStore) Upsert(record ClipRunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if record.RunKey == "" {
		record.RunKey = clipRunKey(record.VideoID, record.StartSec, record.EndSec)
	}
	if err := s.upsertTx(tx, record); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkStatus updates only the status and error.
func (s *ClipRunStore) MarkStatus(runKey string, status ClipRunStatus, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`UPDATE clip_runs SET status = ?, error = ?, updated_at = ? WHERE run_key = ?`,
		status,
		errMsg,
		clipRunNow(),
		runKey,
	)
	return err
}

// UpdateTxtFileID attaches the shared summary txt file to a clip run.
func (s *ClipRunStore) UpdateTxtFileID(videoID string, startSec, endSec int, txtFileID string) error {
	runKey := clipRunKey(videoID, startSec, endSec)
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`UPDATE clip_runs SET txt_file_id = ?, updated_at = ? WHERE run_key = ?`,
		txtFileID,
		clipRunNow(),
		runKey,
	)
	return err
}

// Completed reports whether the run was uploaded successfully.
func (s *ClipRunStore) Completed(runKey string) bool {
	rec, ok := s.Get(runKey)
	if !ok {
		return false
	}
	return rec.Status == ClipRunStatusUploaded && rec.DriveFileID != ""
}

// Close closes the underlying database.
func (s *ClipRunStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
