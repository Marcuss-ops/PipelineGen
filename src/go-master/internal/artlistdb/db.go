// Package artlistdb stores ALL Artlist clips found via search — both downloaded and just indexed.
package artlistdb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// Open opens or creates the Artlist local DB.
func Open(path string) (*ArtlistDB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create DB directory: %w", err)
	}

	db := &ArtlistDB{
		path: path,
		data: &ArtlistData{
			Searches:    make(map[string]SearchResult),
			TotalClips:  0,
			LastUpdated: time.Now().Format(time.RFC3339),
		},
	}

	if _, err := os.Stat(path); err == nil {
		if err := db.load(); err != nil {
			logger.Warn("Failed to load ArtlistDB, starting fresh", zap.Error(err))
		} else {
			logger.Info("ArtlistDB loaded",
				zap.Int("searches", len(db.data.Searches)),
				zap.Int("total_clips", db.data.TotalClips),
			)
		}
	}

	return db, nil
}

// load reads the DB from disk.
func (db *ArtlistDB) load() error {
	data, err := os.ReadFile(db.path)
	if err != nil {
		return fmt.Errorf("failed to read ArtlistDB: %w", err)
	}

	if err := json.Unmarshal(data, &db.data); err != nil {
		return fmt.Errorf("failed to parse ArtlistDB: %w", err)
	}

	if db.data.Searches == nil {
		db.data.Searches = make(map[string]SearchResult)
	}

	return nil
}

// Save writes the DB to disk.
func (db *ArtlistDB) Save() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.data.LastUpdated = time.Now().Format(time.RFC3339)

	data, err := json.MarshalIndent(db.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal ArtlistDB: %w", err)
	}

	return os.WriteFile(db.path, data, 0644)
}
