package stockdb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

type StockDB struct {
	dbPath string
	db     *sql.DB
	mu     sync.RWMutex
}

func Open(dbPath string) (*StockDB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	// Se il path termina in .json, cambiamolo in .sqlite per il nuovo backend
	sqlitePath := strings.TrimSuffix(dbPath, ".json") + ".sqlite"
	
	db, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite: %w", err)
	}

	// Performance: WAL mode e busy timeout per gestire scritture concorrenti
	_, _ = db.Exec("PRAGMA journal_mode=WAL")
	_, _ = db.Exec("PRAGMA synchronous=NORMAL")
	_, _ = db.Exec("PRAGMA busy_timeout=5000") // 5 secondi di attesa prima di fallire

	s := &StockDB{
		dbPath: sqlitePath,
		db:     db,
	}

	if err := s.initSchema(); err != nil {
		return nil, err
	}

	// Migrazione automatica se esiste il vecchio JSON
	if _, err := os.Stat(dbPath); err == nil && strings.HasSuffix(dbPath, ".json") {
		s.migrateFromJSON(dbPath)
	}

	return s, nil
}

func (s *StockDB) initSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS stock_folders (
			topic_slug TEXT PRIMARY KEY,
			drive_id TEXT UNIQUE,
			parent_id TEXT,
			full_path TEXT,
			section TEXT,
			last_synced DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_folders_section ON stock_folders(section)`,
		`CREATE TABLE IF NOT EXISTS stock_clips (
			clip_id TEXT PRIMARY KEY,
			folder_id TEXT,
			filename TEXT,
			source TEXT,
			tags TEXT,
			duration INTEGER,
			status TEXT,
			error_log TEXT,
			updated_at DATETIME,
			FOREIGN KEY(folder_id) REFERENCES stock_folders(drive_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_clips_folder ON stock_clips(folder_id)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("schema error: %w", err)
		}
	}
	return nil
}

func (s *StockDB) migrateFromJSON(jsonPath string) {
	logger.Info("Migrating StockDB from JSON to SQLite", zap.String("path", jsonPath))
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return
	}

	var legacy struct {
		Folders []StockFolderEntry `json:"folders"`
		Clips   []StockClipEntry   `json:"clips"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return
	}

	_ = s.BulkUpsertFolders(legacy.Folders)
	_ = s.BulkUpsertClips(legacy.Clips)

	// Rinomina il vecchio file per evitare re-migrazioni
	_ = os.Rename(jsonPath, jsonPath+".bak")
	logger.Info("Migration completed, JSON backed up to .bak")
}

func (s *StockDB) FindFolderByTopic(topic string) (*StockFolderEntry, error) {
	slug := normalizeSlug(topic)
	
	// Priorità stock section
	query := `SELECT topic_slug, drive_id, parent_id, full_path, section, last_synced 
	          FROM stock_folders 
			  WHERE (topic_slug = ? OR full_path LIKE ?)
			  ORDER BY CASE WHEN section = 'stock' THEN 0 ELSE 1 END, topic_slug ASC LIMIT 1`
	
	var f StockFolderEntry
	err := s.db.QueryRow(query, slug, "%"+topic+"%").Scan(
		&f.TopicSlug, &f.DriveID, &f.ParentID, &f.FullPath, &f.Section, &f.LastSynced,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

func (s *StockDB) UpsertFolder(f StockFolderEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if f.TopicSlug == "" {
		f.TopicSlug = normalizeSlug(f.FullPath)
	}
	query := `INSERT INTO stock_folders (topic_slug, drive_id, parent_id, full_path, section, last_synced)
	          VALUES (?, ?, ?, ?, ?, ?)
			  ON CONFLICT(topic_slug) DO UPDATE SET
			    drive_id=excluded.drive_id, full_path=excluded.full_path, last_synced=excluded.last_synced`
	_, err := s.db.Exec(query, f.TopicSlug, f.DriveID, f.ParentID, f.FullPath, f.Section, time.Now())
	return err
}

func (s *StockDB) BulkUpsertFolders(folders []StockFolderEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, _ := tx.Prepare(`INSERT INTO stock_folders (topic_slug, drive_id, parent_id, full_path, section, last_synced)
	                       VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(topic_slug) DO UPDATE SET drive_id=excluded.drive_id`)
	for _, f := range folders {
		if f.TopicSlug == "" { f.TopicSlug = normalizeSlug(f.FullPath) }
		_, _ = stmt.Exec(f.TopicSlug, f.DriveID, f.ParentID, f.FullPath, f.Section, time.Now())
	}
	return tx.Commit()
}

func (s *StockDB) BulkUpsertClips(clips []StockClipEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, _ := tx.Prepare(`INSERT INTO stock_clips (clip_id, folder_id, filename, source, tags, duration, status, updated_at)
	                       VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(clip_id) DO UPDATE SET status=excluded.status`)
	for _, c := range clips {
		tags := strings.Join(c.Tags, ",")
		_, _ = stmt.Exec(c.ClipID, c.FolderID, c.Filename, c.Source, tags, c.Duration, c.Status, time.Now())
	}
	return tx.Commit()
}

func (s *StockDB) GetClipsForFolder(folderID string) ([]StockClipEntry, error) {
	rows, err := s.db.Query("SELECT clip_id, folder_id, filename, source, tags, duration FROM stock_clips WHERE folder_id = ?", folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []StockClipEntry
	for rows.Next() {
		var c StockClipEntry
		var tagsStr string
		_ = rows.Scan(&c.ClipID, &c.FolderID, &c.Filename, &c.Source, &tagsStr, &c.Duration)
		c.Tags = strings.Split(tagsStr, ",")
		clips = append(clips, c)
	}
	return clips, nil
}

func (s *StockDB) SearchClipsByTags(tags []string) ([]StockClipEntry, error) {
	if len(tags) == 0 { return nil, nil }
	
	// Costruiamo una query orribile ma veloce per SQLite
	placeholders := make([]string, len(tags))
	args := make([]interface{}, len(tags))
	for i, t := range tags {
		placeholders[i] = "tags LIKE ?"
		args[i] = "%" + t + "%"
	}
	query := "SELECT clip_id, folder_id, filename, source, tags, duration FROM stock_clips WHERE " + strings.Join(placeholders, " OR ")
	
	rows, err := s.db.Query(query, args...)
	if err != nil { return nil, err }
	defer rows.Close()

	var clips []StockClipEntry
	for rows.Next() {
		var c StockClipEntry
		var tagsStr string
		_ = rows.Scan(&c.ClipID, &c.FolderID, &c.Filename, &c.Source, &tagsStr, &c.Duration)
		c.Tags = strings.Split(tagsStr, ",")
		clips = append(clips, c)
	}
	return clips, nil
}

func (s *StockDB) SearchClipsByTagsInSection(tags []string, section string) ([]StockClipEntry, error) {
	if len(tags) == 0 { return nil, nil }
	
	tagFilters := make([]string, len(tags))
	args := make([]interface{}, 0, len(tags)+1)
	for i, t := range tags {
		tagFilters[i] = "c.tags LIKE ?"
		args = append(args, "%"+t+"%")
	}
	args = append(args, section)

	query := `SELECT c.clip_id, c.folder_id, c.filename, c.source, c.tags, c.duration 
	          FROM stock_clips c
			  JOIN stock_folders f ON c.folder_id = f.drive_id
			  WHERE f.section = ? AND (` + strings.Join(tagFilters, " OR ") + ")"
	
	// Riordiniamo gli argomenti: section è il primo nel JOIN/WHERE logicamente se lo mettiamo così
	// In realtà nella query sopra, section è il primo parametro.
	actualArgs := append([]interface{}{section}, args[:len(args)-1]...)

	rows, err := s.db.Query(query, actualArgs...)
	if err != nil { return nil, err }
	defer rows.Close()

	var clips []StockClipEntry
	for rows.Next() {
		var c StockClipEntry
		var tagsStr string
		_ = rows.Scan(&c.ClipID, &c.FolderID, &c.Filename, &c.Source, &tagsStr, &c.Duration)
		c.Tags = strings.Split(tagsStr, ",")
		clips = append(clips, c)
	}
	return clips, nil
}

func (s *StockDB) FindFolderByDriveID(id string) (*StockFolderEntry, error) {
	var f StockFolderEntry
	err := s.db.QueryRow(`SELECT topic_slug, drive_id, parent_id, full_path, section, last_synced 
	                      FROM stock_folders WHERE drive_id = ?`, id).Scan(
		&f.TopicSlug, &f.DriveID, &f.ParentID, &f.FullPath, &f.Section, &f.LastSynced,
	)
	if err == sql.ErrNoRows { return nil, nil }
	return &f, err
}

func (s *StockDB) FindFolderByTopicInSection(topic, section string) (*StockFolderEntry, error) {
	slug := normalizeSlug(topic)
	var f StockFolderEntry
	err := s.db.QueryRow(`SELECT topic_slug, drive_id, parent_id, full_path, section, last_synced 
	                      FROM stock_folders WHERE section = ? AND (topic_slug = ? OR full_path LIKE ?)`, 
						  section, slug, "%"+topic+"%").Scan(
		&f.TopicSlug, &f.DriveID, &f.ParentID, &f.FullPath, &f.Section, &f.LastSynced,
	)
	if err == sql.ErrNoRows { return nil, nil }
	return &f, err
}

func (s *StockDB) GetFoldersBySection(section string) ([]StockFolderEntry, error) {
	rows, err := s.db.Query(`SELECT topic_slug, drive_id, parent_id, full_path, section, last_synced 
	                      FROM stock_folders WHERE section = ?`, section)
	if err != nil { return nil, err }
	defer rows.Close()
	var res []StockFolderEntry
	for rows.Next() {
		var f StockFolderEntry
		_ = rows.Scan(&f.TopicSlug, &f.DriveID, &f.ParentID, &f.FullPath, &f.Section, &f.LastSynced)
		res = append(res, f)
	}
	return res, nil
}

func (s *StockDB) GetUnusedClips(section string, usedIDs []string) ([]StockClipEntry, error) {
	usedMap := make(map[string]bool)
	for _, id := range usedIDs {
		usedMap[id] = true
	}
	all, err := s.GetAllClips()
	if err != nil { return nil, err }
	var res []StockClipEntry
	for _, c := range all {
		if !usedMap[c.ClipID] {
			res = append(res, c)
		}
	}
	return res, nil
}

func (s *StockDB) Close() error {
	return s.db.Close()
}

// Helper methods per compatibilità interfaccia esistente
func (s *StockDB) GetAllFolders() ([]StockFolderEntry, error) {
	rows, err := s.db.Query("SELECT topic_slug, drive_id, parent_id, full_path, section, last_synced FROM stock_folders")
	if err != nil { return nil, err }
	defer rows.Close()
	var res []StockFolderEntry
	for rows.Next() {
		var f StockFolderEntry
		_ = rows.Scan(&f.TopicSlug, &f.DriveID, &f.ParentID, &f.FullPath, &f.Section, &f.LastSynced)
		res = append(res, f)
	}
	return res, nil
}

func (s *StockDB) UpsertClip(c StockClipEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tags := strings.Join(c.Tags, ",")
	query := `INSERT INTO stock_clips (clip_id, folder_id, filename, source, tags, duration, status, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(clip_id) DO UPDATE SET updated_at=excluded.updated_at`
	_, err := s.db.Exec(query, c.ClipID, c.FolderID, c.Filename, c.Source, tags, c.Duration, c.Status, time.Now())
	return err
}

func (s *StockDB) GetAllClips() ([]StockClipEntry, error) {
	rows, err := s.db.Query("SELECT clip_id, folder_id, filename, source, tags, duration FROM stock_clips")
	if err != nil { return nil, err }
	defer rows.Close()
	var res []StockClipEntry
	for rows.Next() {
		var c StockClipEntry
		var tagsStr string
		_ = rows.Scan(&c.ClipID, &c.FolderID, &c.Filename, &c.Source, &tagsStr, &c.Duration)
		c.Tags = strings.Split(tagsStr, ",")
		res = append(res, c)
	}
	return res, nil
}

func (s *StockDB) DeleteClipByID(id string) error {
	_, err := s.db.Exec("DELETE FROM stock_clips WHERE clip_id = ?", id)
	return err
}

func (s *StockDB) DeduplicateByFolderAndFilename() ([]string, error) {
	// Trova duplicati (stesso folder e filename, mantiene quello con ID minore)
	rows, err := s.db.Query(`
		SELECT clip_id FROM stock_clips 
		WHERE rowid NOT IN (
			SELECT MIN(rowid) FROM stock_clips GROUP BY folder_id, filename
		)`)
	if err != nil { return nil, err }
	defer rows.Close()
	
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	
	if len(ids) > 0 {
		_, _ = s.db.Exec(`
			DELETE FROM stock_clips 
			WHERE rowid NOT IN (
				SELECT MIN(rowid) FROM stock_clips GROUP BY folder_id, filename
			)`)
	}
	return ids, nil
}

func (s *StockDB) GetStats() map[string]int {
	stats := make(map[string]int)
	var folders, clips, artlist, stock int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM stock_folders").Scan(&folders)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM stock_clips").Scan(&clips)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM stock_clips WHERE source='artlist'").Scan(&artlist)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM stock_clips WHERE source='stock'").Scan(&stock)
	stats["folders"] = folders
	stats["clips"] = clips
	stats["artlist_clips"] = artlist
	stats["stock_clips"] = stock
	return stats
}
