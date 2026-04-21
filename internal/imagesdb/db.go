// Package imagesdb stores image candidates selected for script documents.
package imagesdb

import (
	"database/sql"
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

// ImageRecord stores the best image selected for a given entity.
type ImageRecord struct {
	Entity         string    `json:"entity"`
	Query          string    `json:"query"`
	Source         string    `json:"source"`
	Title          string    `json:"title,omitempty"`
	PageURL        string    `json:"page_url,omitempty"`
	ImageURL       string    `json:"image_url"`
	ThumbnailURL   string    `json:"thumbnail_url,omitempty"`
	LocalPath      string    `json:"local_path,omitempty"`
	MimeType       string    `json:"mime_type,omitempty"`
	FileSizeBytes  int64     `json:"file_size_bytes,omitempty"`
	AssetHash      string    `json:"asset_hash,omitempty"`
	Width          int       `json:"width,omitempty"`
	Height         int       `json:"height,omitempty"`
	License        string    `json:"license,omitempty"`
	RelevanceScore float64   `json:"relevance_score,omitempty"`
	UsedCount      int       `json:"used_count,omitempty"`
	VideoID        string    `json:"video_id,omitempty"`
	ChapterIndex   int       `json:"chapter_index,omitempty"`
	SegmentKey     string    `json:"segment_key,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	LastUsedAt     time.Time `json:"last_used_at,omitempty"`
	DownloadedAt   time.Time `json:"downloaded_at,omitempty"`
}

// ImageDB is a local SQLite-backed cache of image selections.
type ImageDB struct {
	path string
	db   *sql.DB
	mu   sync.RWMutex
}

// Open opens or creates the images DB.
func Open(path string) (*ImageDB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create images db directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open images sqlite db: %w", err)
	}
	_, _ = db.Exec(`PRAGMA journal_mode=WAL;`)
	_, _ = db.Exec(`PRAGMA synchronous=NORMAL;`)
	_, _ = db.Exec(`PRAGMA foreign_keys=ON;`)
	_, _ = db.Exec(`PRAGMA busy_timeout=5000;`)

	store := &ImageDB{path: path, db: db}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (db *ImageDB) initSchema() error {
	query := `CREATE TABLE IF NOT EXISTS images (
		entity TEXT PRIMARY KEY,
		query TEXT,
		source TEXT,
		title TEXT,
		page_url TEXT,
		image_url TEXT NOT NULL,
		thumbnail_url TEXT,
		local_path TEXT,
		mime_type TEXT,
		file_size_bytes INTEGER,
		asset_hash TEXT,
		width INTEGER,
		height INTEGER,
		license TEXT,
		relevance_score REAL,
		used_count INTEGER DEFAULT 0,
		video_id TEXT,
		chapter_index INTEGER,
		segment_key TEXT,
		created_at DATETIME,
		updated_at DATETIME,
		last_used_at DATETIME,
		downloaded_at DATETIME
	)`
	if _, err := db.db.Exec(query); err != nil {
		return fmt.Errorf("images schema error: %w", err)
	}
	return nil
}

// Close closes the DB.
func (db *ImageDB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.db == nil {
		return nil
	}
	return db.db.Close()
}

// Get returns the cached image for an entity.
func (db *ImageDB) Get(entity string) (*ImageRecord, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.db == nil {
		return nil, false
	}
	row := db.db.QueryRow(`SELECT entity, query, source, title, page_url, image_url, thumbnail_url, local_path, mime_type, file_size_bytes, asset_hash, width, height, license, relevance_score, used_count, video_id, chapter_index, segment_key, created_at, updated_at, last_used_at, downloaded_at
		FROM images WHERE entity = ?`, normalizeKey(entity))

	var rec ImageRecord
	var createdAt, updatedAt, lastUsedAt, downloadedAt sql.NullTime
	var title, pageURL, thumbnailURL, localPath, mimeType, license, source, query, imageURL, assetHash, videoID, segmentKey sql.NullString
	var width, height, chapterIndex, fileSizeBytes sql.NullInt64
	var relevance sql.NullFloat64
	var usedCount sql.NullInt64
	if err := row.Scan(&rec.Entity, &query, &source, &title, &pageURL, &imageURL, &thumbnailURL, &localPath, &mimeType, &fileSizeBytes, &assetHash, &width, &height, &license, &relevance, &usedCount, &videoID, &chapterIndex, &segmentKey, &createdAt, &updatedAt, &lastUsedAt, &downloadedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, false
		}
		logger.Debug("imagesdb get failed", zap.String("entity", entity), zap.Error(err))
		return nil, false
	}

	rec.Query = query.String
	rec.Source = source.String
	rec.Title = title.String
	rec.PageURL = pageURL.String
	rec.ImageURL = imageURL.String
	rec.ThumbnailURL = thumbnailURL.String
	rec.LocalPath = localPath.String
	rec.MimeType = mimeType.String
	rec.FileSizeBytes = fileSizeBytes.Int64
	rec.AssetHash = assetHash.String
	rec.Width = int(width.Int64)
	rec.Height = int(height.Int64)
	rec.License = license.String
	rec.RelevanceScore = relevance.Float64
	rec.UsedCount = int(usedCount.Int64)
	rec.VideoID = videoID.String
	rec.ChapterIndex = int(chapterIndex.Int64)
	rec.SegmentKey = segmentKey.String
	if createdAt.Valid {
		rec.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		rec.UpdatedAt = updatedAt.Time
	}
	if lastUsedAt.Valid {
		rec.LastUsedAt = lastUsedAt.Time
	}
	if downloadedAt.Valid {
		rec.DownloadedAt = downloadedAt.Time
	}
	return &rec, true
}

// Upsert stores or updates an image record.
func (db *ImageDB) Upsert(rec ImageRecord) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.db == nil {
		return fmt.Errorf("images db is not open")
	}
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	key := normalizeKey(rec.Entity)
	if key == "" {
		return fmt.Errorf("entity is required")
	}
	if rec.ImageURL == "" {
		return fmt.Errorf("image_url is required")
	}

	_, err := db.db.Exec(`INSERT INTO images (
		entity, query, source, title, page_url, image_url, thumbnail_url, local_path, mime_type, file_size_bytes, asset_hash, width, height, license, relevance_score, used_count, video_id, chapter_index, segment_key, created_at, updated_at, last_used_at, downloaded_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(entity) DO UPDATE SET
		query=excluded.query,
		source=excluded.source,
		title=excluded.title,
		page_url=excluded.page_url,
		image_url=excluded.image_url,
		thumbnail_url=excluded.thumbnail_url,
		local_path=excluded.local_path,
		mime_type=excluded.mime_type,
		file_size_bytes=excluded.file_size_bytes,
		asset_hash=excluded.asset_hash,
		width=excluded.width,
		height=excluded.height,
		license=excluded.license,
		relevance_score=excluded.relevance_score,
		video_id=excluded.video_id,
		chapter_index=excluded.chapter_index,
		segment_key=excluded.segment_key,
		updated_at=excluded.updated_at,
		downloaded_at=excluded.downloaded_at
	`, key, rec.Query, rec.Source, rec.Title, rec.PageURL, rec.ImageURL, rec.ThumbnailURL, rec.LocalPath, rec.MimeType, rec.FileSizeBytes, rec.AssetHash, rec.Width, rec.Height, rec.License, rec.RelevanceScore, rec.UsedCount, rec.VideoID, rec.ChapterIndex, rec.SegmentKey, rec.CreatedAt, rec.UpdatedAt, rec.LastUsedAt, rec.DownloadedAt)
	return err
}

// Touch increments usage for an entity and stores the latest context.
func (db *ImageDB) Touch(rec ImageRecord) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.db == nil {
		return fmt.Errorf("images db is not open")
	}
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	rec.LastUsedAt = now
	key := normalizeKey(rec.Entity)
	if key == "" {
		return fmt.Errorf("entity is required")
	}
	_, err := db.db.Exec(`INSERT INTO images (
		entity, query, source, title, page_url, image_url, thumbnail_url, local_path, mime_type, file_size_bytes, asset_hash, width, height, license, relevance_score, used_count, video_id, chapter_index, segment_key, created_at, updated_at, last_used_at, downloaded_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(entity) DO UPDATE SET
		query=excluded.query,
		source=excluded.source,
		title=excluded.title,
		page_url=excluded.page_url,
		image_url=excluded.image_url,
		thumbnail_url=excluded.thumbnail_url,
		local_path=excluded.local_path,
		mime_type=excluded.mime_type,
		file_size_bytes=excluded.file_size_bytes,
		asset_hash=excluded.asset_hash,
		width=excluded.width,
		height=excluded.height,
		license=excluded.license,
		relevance_score=excluded.relevance_score,
		used_count=images.used_count + 1,
		video_id=excluded.video_id,
		chapter_index=excluded.chapter_index,
		segment_key=excluded.segment_key,
		updated_at=excluded.updated_at,
		last_used_at=excluded.last_used_at,
		downloaded_at=excluded.downloaded_at
	`, key, rec.Query, rec.Source, rec.Title, rec.PageURL, rec.ImageURL, rec.ThumbnailURL, rec.LocalPath, rec.MimeType, rec.FileSizeBytes, rec.AssetHash, rec.Width, rec.Height, rec.License, rec.RelevanceScore, rec.UsedCount, rec.VideoID, rec.ChapterIndex, rec.SegmentKey, rec.CreatedAt, rec.UpdatedAt, rec.LastUsedAt, rec.DownloadedAt)
	return err
}

// ListAll returns all cached image records.
func (db *ImageDB) ListAll() ([]ImageRecord, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.db == nil {
		return nil, fmt.Errorf("images db is not open")
	}
	rows, err := db.db.Query(`SELECT entity, query, source, title, page_url, image_url, thumbnail_url, local_path, mime_type, file_size_bytes, asset_hash, width, height, license, relevance_score, used_count, video_id, chapter_index, segment_key, created_at, updated_at, last_used_at, downloaded_at
		FROM images ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ImageRecord
	for rows.Next() {
		var rec ImageRecord
		var createdAt, updatedAt, lastUsedAt, downloadedAt sql.NullTime
		var title, pageURL, thumbnailURL, localPath, mimeType, license, source, query, imageURL, assetHash, videoID, segmentKey sql.NullString
		var width, height, chapterIndex, fileSizeBytes sql.NullInt64
		var relevance sql.NullFloat64
		var usedCount sql.NullInt64
		if err := rows.Scan(&rec.Entity, &query, &source, &title, &pageURL, &imageURL, &thumbnailURL, &localPath, &mimeType, &fileSizeBytes, &assetHash, &width, &height, &license, &relevance, &usedCount, &videoID, &chapterIndex, &segmentKey, &createdAt, &updatedAt, &lastUsedAt, &downloadedAt); err != nil {
			return nil, err
		}
		rec.Query = query.String
		rec.Source = source.String
		rec.Title = title.String
		rec.PageURL = pageURL.String
		rec.ImageURL = imageURL.String
		rec.ThumbnailURL = thumbnailURL.String
		rec.LocalPath = localPath.String
		rec.MimeType = mimeType.String
		rec.FileSizeBytes = fileSizeBytes.Int64
		rec.AssetHash = assetHash.String
		rec.Width = int(width.Int64)
		rec.Height = int(height.Int64)
		rec.License = license.String
		rec.RelevanceScore = relevance.Float64
		rec.UsedCount = int(usedCount.Int64)
		rec.VideoID = videoID.String
		rec.ChapterIndex = int(chapterIndex.Int64)
		rec.SegmentKey = segmentKey.String
		if createdAt.Valid {
			rec.CreatedAt = createdAt.Time
		}
		if updatedAt.Valid {
			rec.UpdatedAt = updatedAt.Time
		}
		if lastUsedAt.Valid {
			rec.LastUsedAt = lastUsedAt.Time
		}
		if downloadedAt.Valid {
			rec.DownloadedAt = downloadedAt.Time
		}
		out = append(out, rec)
	}
	return out, nil
}

func normalizeKey(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
