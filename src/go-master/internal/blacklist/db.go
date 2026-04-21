// Package blacklist provides persistent blacklist management for videos
package blacklist

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type Record struct {
	VideoID       string    `json:"video_id"`
	Title         string    `json:"title,omitempty"`
	Channel       string    `json:"channel,omitempty"`
	Reason        string    `json:"reason"`
	Score         float64   `json:"score"`
	BlacklistedAt time.Time `json:"blacklisted_at"`
	AddedBy       string    `json:"added_by,omitempty"`
}

type Database struct {
	path    string
	mu      sync.RWMutex
	records map[string]*Record
}

func Open(path string) (*Database, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create blacklist directory: %w", err)
	}

	db := &Database{
		path:    path,
		records: make(map[string]*Record),
	}

	if err := db.load(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load blacklist: %w", err)
		}
		logger.Info("Creating new blacklist database", zap.String("path", path))
		db.records = make(map[string]*Record)
		if err := db.save(); err != nil {
			return nil, fmt.Errorf("failed to create blacklist: %w", err)
		}
	}

	logger.Info("Blacklist database loaded", zap.Int("records", len(db.records)))
	return db, nil
}

func (db *Database) load() error {
	data, err := os.ReadFile(db.path)
	if err != nil {
		return err
	}

	var records []Record
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("failed to parse blacklist: %w", err)
	}

	db.records = make(map[string]*Record)
	for i := range records {
		rec := &records[i]
		db.records[rec.VideoID] = rec
	}

	return nil
}

func (db *Database) save() error {
	records := make([]Record, 0, len(db.records))
	for _, rec := range db.records {
		records = append(records, *rec)
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal blacklist: %w", err)
	}

	if err := os.WriteFile(db.path, data, 0644); err != nil {
		return fmt.Errorf("failed to write blacklist: %w", err)
	}

	return nil
}

func (db *Database) Add(videoID, title, channel, reason string, score float64, addedBy string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, exists := db.records[videoID]; exists {
		return fmt.Errorf("video already blacklisted: %s", videoID)
	}

	record := &Record{
		VideoID:       videoID,
		Title:         title,
		Channel:       channel,
		Reason:        reason,
		Score:         score,
		AddedBy:       addedBy,
		BlacklistedAt: time.Now(),
	}

	db.records[videoID] = record

	if err := db.save(); err != nil {
		return fmt.Errorf("failed to save blacklist: %w", err)
	}

	logger.Info("Video blacklisted",
		zap.String("video_id", videoID),
		zap.String("reason", reason),
	)

	return nil
}

func (db *Database) Remove(videoID string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, exists := db.records[videoID]; !exists {
		return fmt.Errorf("video not in blacklist: %s", videoID)
	}

	delete(db.records, videoID)

	if err := db.save(); err != nil {
		return fmt.Errorf("failed to save blacklist: %w", err)
	}

	logger.Info("Video removed from blacklist", zap.String("video_id", videoID))
	return nil
}

func (db *Database) IsBlacklisted(videoID string) bool {
	db.mu.RLock()
	defer db.mu.RUnlock()

	_, exists := db.records[videoID]
	return exists
}

func (db *Database) Get(videoID string) (*Record, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	record, exists := db.records[videoID]
	if !exists {
		return nil, fmt.Errorf("video not in blacklist: %s", videoID)
	}

	return record, nil
}

func (db *Database) GetAll() []Record {
	db.mu.RLock()
	defer db.mu.RUnlock()

	records := make([]Record, 0, len(db.records))
	for _, rec := range db.records {
		records = append(records, *rec)
	}

	return records
}

func (db *Database) GetByReason(reason string) []Record {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var records []Record
	for _, rec := range db.records {
		if rec.Reason == reason {
			records = append(records, *rec)
		}
	}

	return records
}

func (db *Database) GetCount() int {
	db.mu.RLock()
	defer db.mu.RUnlock()

	return len(db.records)
}

func (db *Database) Clear() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.records = make(map[string]*Record)

	if err := db.save(); err != nil {
		return fmt.Errorf("failed to clear blacklist: %w", err)
	}

	logger.Info("Blacklist cleared")
	return nil
}

func (db *Database) Export(path string) error {
	records := db.GetAll()

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal blacklist: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

func (db *Database) Import(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read blacklist file: %w", err)
	}

	var records []Record
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("failed to parse blacklist file: %w", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	for _, rec := range records {
		db.records[rec.VideoID] = &rec
	}

	if err := db.save(); err != nil {
		return fmt.Errorf("failed to save imported blacklist: %w", err)
	}

	logger.Info("Blacklist imported", zap.Int("records", len(records)))
	return nil
}
