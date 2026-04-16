// Package stockdb provides JSON-backed stock folder and clip management.
// This is the Single Source of Truth for what exists on Google Drive.
package stockdb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// StockDB manages stock folders and clips in JSON — Single Source of Truth
type StockDB struct {
	dbPath   string
	mu       sync.RWMutex
	data     *StockDatabase
}

// StockDatabase is the root JSON structure
type StockDatabase struct {
	LastSynced time.Time        `json:"last_synced"`
	Folders    []StockFolderEntry `json:"folders"`
	Clips      []StockClipEntry   `json:"clips"`
}

// StockFolderEntry matches the exact schema
type StockFolderEntry struct {
	TopicSlug  string    `json:"topic_slug"`  // PK, e.g. "gervonta-davis"
	DriveID    string    `json:"drive_id"`    // Google Drive folder ID
	ParentID   string    `json:"parent_id"`   // Parent folder Drive ID
	FullPath   string    `json:"full_path"`   // e.g. "stock/Boxe/GervontaDavis"
	Section    string    `json:"section"`     // "stock" or "clips" — KEY for fast lookup
	LastSynced time.Time `json:"last_synced"` // When last scanned
}

// StockClipEntry matches the exact schema
type StockClipEntry struct {
	ClipID   string   `json:"clip_id"`   // PK, Drive file ID
	FolderID string   `json:"folder_id"` // FK → stock_folders.drive_id
	Filename string   `json:"filename"`  // e.g. "knockout_garcia.mp4"
	Source   string   `json:"source"`    // "artlist" or "stock"
	Tags     []string `json:"tags"`      // comma-separated keywords
	Duration int      `json:"duration"`  // seconds
}

// Open opens or creates the stock database
func Open(dbPath string) (*StockDB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	s := &StockDB{
		dbPath: dbPath,
		data: &StockDatabase{
			Folders: []StockFolderEntry{},
			Clips:   []StockClipEntry{},
		},
	}

	// Load existing data if file exists
	if _, err := os.Stat(dbPath); err == nil {
		if err := s.load(); err != nil {
			logger.Warn("Failed to load existing DB, starting fresh", zap.Error(err))
		}
	}

	return s, nil
}

func (s *StockDB) load() error {
	data, err := os.ReadFile(s.dbPath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.data)
}

func (s *StockDB) save() error {
	s.data.LastSynced = time.Now()
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}
	return os.WriteFile(s.dbPath, data, 0644)
}

// FindFolderByTopic searches for a folder matching the topic keywords
// PRIORITY: Stock section first (for main topic resolution)
// Returns instantly from DB — no Drive API call needed
func (s *StockDB) FindFolderByTopic(topic string) (*StockFolderEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	slug := normalizeSlug(topic)

	// 1. Try exact slug match in Stock section FIRST (faster, primary)
	for i := range s.data.Folders {
		if s.data.Folders[i].Section == "stock" && s.data.Folders[i].TopicSlug == slug {
			return &s.data.Folders[i], nil
		}
	}

	// 2. Try partial match on full_path in Stock section
	keywords := strings.Fields(strings.ToLower(topic))
	for _, kw := range keywords {
		for i := range s.data.Folders {
			if s.data.Folders[i].Section == "stock" &&
				strings.Contains(strings.ToLower(s.data.Folders[i].FullPath), kw) {
				return &s.data.Folders[i], nil
			}
		}
	}

	// 3. Fallback to Clips section
	for i := range s.data.Folders {
		if s.data.Folders[i].Section == "clips" && s.data.Folders[i].TopicSlug == slug {
			return &s.data.Folders[i], nil
		}
	}
	for _, kw := range keywords {
		for i := range s.data.Folders {
			if s.data.Folders[i].Section == "clips" &&
				strings.Contains(strings.ToLower(s.data.Folders[i].FullPath), kw) {
				return &s.data.Folders[i], nil
			}
		}
	}

	return nil, nil // Not found
}

// FindFolderByTopicInSection searches ONLY in a specific section (stock or clips)
// Much faster when you know which section to search
func (s *StockDB) FindFolderByTopicInSection(topic, section string) (*StockFolderEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	slug := normalizeSlug(topic)

	// Exact match
	for i := range s.data.Folders {
		if s.data.Folders[i].Section == section && s.data.Folders[i].TopicSlug == slug {
			return &s.data.Folders[i], nil
		}
	}

	// Partial match
	keywords := strings.Fields(strings.ToLower(topic))
	for _, kw := range keywords {
		for i := range s.data.Folders {
			if s.data.Folders[i].Section == section &&
				strings.Contains(strings.ToLower(s.data.Folders[i].FullPath), kw) {
				return &s.data.Folders[i], nil
			}
		}
	}

	return nil, nil
}

// GetFoldersBySection returns all folders in a specific section
func (s *StockDB) GetFoldersBySection(section string) ([]StockFolderEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []StockFolderEntry
	for i := range s.data.Folders {
		if s.data.Folders[i].Section == section {
			result = append(result, s.data.Folders[i])
		}
	}
	return result, nil
}

// FindFolderByDriveID finds a folder by its Drive ID
func (s *StockDB) FindFolderByDriveID(driveID string) (*StockFolderEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range s.data.Folders {
		if s.data.Folders[i].DriveID == driveID {
			return &s.data.Folders[i], nil
		}
	}
	return nil, fmt.Errorf("folder not found")
}

// UpsertFolder inserts or updates a folder (for Drive sync)
func (s *StockDB) UpsertFolder(folder StockFolderEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if folder.TopicSlug == "" {
		folder.TopicSlug = normalizeSlug(folder.FullPath)
	}
	folder.LastSynced = time.Now()

	// Update existing or append
	for i := range s.data.Folders {
		if s.data.Folders[i].DriveID == folder.DriveID {
			s.data.Folders[i] = folder
			return s.save()
		}
	}

	s.data.Folders = append(s.data.Folders, folder)
	return s.save()
}

// UpsertClip inserts or updates a clip (for Drive sync)
func (s *StockDB) UpsertClip(clip StockClipEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update existing or append
	for i := range s.data.Clips {
		if s.data.Clips[i].ClipID == clip.ClipID {
			s.data.Clips[i] = clip
			return s.save()
		}
	}

	s.data.Clips = append(s.data.Clips, clip)
	return s.save()
}

// DeleteClipsNotInFolder removes clips from a folder that are no longer in Drive
func (s *StockDB) DeleteClipsNotInFolder(folderDriveID string, keepClipIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keepMap := make(map[string]bool)
	for _, id := range keepClipIDs {
		keepMap[id] = true
	}

	var remaining []StockClipEntry
	for _, c := range s.data.Clips {
		if c.FolderID != folderDriveID || keepMap[c.ClipID] {
			remaining = append(remaining, c)
		}
	}

	s.data.Clips = remaining
	return s.save()
}

// GetClipsForFolder returns all clips in a folder (instant from DB)
func (s *StockDB) GetClipsForFolder(folderDriveID string) ([]StockClipEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var clips []StockClipEntry
	for _, c := range s.data.Clips {
		if c.FolderID == folderDriveID {
			clips = append(clips, c)
		}
	}
	return clips, nil
}
// SearchClipsByTagsInSection searches clips matching tags ONLY in a specific section
func (s *StockDB) SearchClipsByTagsInSection(tags []string, section string) ([]StockClipEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Get all folders in that section for fast lookup
	sectionFolders := make(map[string]bool)
	for _, f := range s.data.Folders {
		if f.Section == section {
			sectionFolders[f.DriveID] = true
		}
	}

	clipsMap := make(map[string]StockClipEntry)
	for _, tag := range tags {
		tagLower := strings.ToLower(tag)
		for _, c := range s.data.Clips {
			// Only include if folder is in the requested section
			if sectionFolders[c.FolderID] {
				// Check if any of the clip's tags match the search tag
				tagFound := false
				for _, clipTag := range c.Tags {
					if strings.Contains(strings.ToLower(clipTag), tagLower) {
						tagFound = true
						break
					}
				}

				if tagFound || strings.Contains(strings.ToLower(c.Filename), tagLower) {
					clipsMap[c.ClipID] = c
				}
			}
		}
	}

	var clips []StockClipEntry
	for _, c := range clipsMap {
		clips = append(clips, c)
	}
	return clips, nil
}

// SearchClipsByTags searches clips matching tags across all folders
func (s *StockDB) SearchClipsByTags(tags []string) ([]StockClipEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clipsMap := make(map[string]StockClipEntry)

	for _, tag := range tags {
		tagLower := strings.ToLower(tag)
		for _, c := range s.data.Clips {
			tagFound := false
			for _, clipTag := range c.Tags {
				if strings.Contains(strings.ToLower(clipTag), tagLower) {
					tagFound = true
					break
				}
			}
			if tagFound {
				clipsMap[c.ClipID] = c
			}
		}
	}

	var clips []StockClipEntry
	for _, c := range clipsMap {
		clips = append(clips, c)
	}
	return clips, nil
}

// GetUnusedClips returns clips not yet associated with any entity
func (s *StockDB) GetUnusedClips(folderDriveID string, usedClipIDs []string) ([]StockClipEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	usedMap := make(map[string]bool)
	for _, id := range usedClipIDs {
		usedMap[id] = true
	}

	var clips []StockClipEntry
	for _, c := range s.data.Clips {
		if folderDriveID == "" || c.FolderID == folderDriveID {
			if !usedMap[c.ClipID] {
				clips = append(clips, c)
			}
		}
	}
	return clips, nil
}

// GetAllFolders returns all folders (for admin/debug)
func (s *StockDB) GetAllFolders() ([]StockFolderEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Folders, nil
}

// GetAllClips returns all clips (for admin/debug)
func (s *StockDB) GetAllClips() ([]StockClipEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Clips, nil
}

// GetStats returns database statistics
func (s *StockDB) GetStats() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := map[string]int{
		"folders":      len(s.data.Folders),
		"clips":        len(s.data.Clips),
		"artlist_clips": 0,
		"stock_clips":   0,
	}

	for _, c := range s.data.Clips {
		if c.Source == "artlist" {
			stats["artlist_clips"]++
		} else {
			stats["stock_clips"]++
		}
	}

	return stats
}

// Close is a no-op for JSON backend
func (s *StockDB) Close() error {
	return s.save()
}

// NormalizeSlug exports the normalizeSlug function for external use
func NormalizeSlug(s string) string {
	return normalizeSlug(s)
}

// normalizeSlug converts a topic string to a URL-safe slug
func normalizeSlug(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "_", "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	return s
}

// PopulateDefaults inserts generic default stock folders
func (s *StockDB) PopulateDefaults() error {
	defaults := []StockFolderEntry{
		{TopicSlug: "stock-category1", DriveID: "default_1", ParentID: "", FullPath: "Stock/Category1"},
		{TopicSlug: "stock-category2", DriveID: "default_2", ParentID: "", FullPath: "Stock/Category2"},
	}

	for _, f := range defaults {
		exists := false
		for _, existing := range s.data.Folders {
			if existing.DriveID == f.DriveID {
				exists = true
				break
			}
		}
		if !exists {
			if err := s.UpsertFolder(f); err != nil {
				logger.Warn("Failed to insert default folder", zap.String("name", f.FullPath), zap.Error(err))
			}
		}
	}

	logger.Info("Default stock folders populated")
	return nil
}

// BulkUpsertFolders inserts or updates multiple folders at once
func (s *StockDB) BulkUpsertFolders(folders []StockFolderEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, folder := range folders {
		if folder.TopicSlug == "" {
			folder.TopicSlug = normalizeSlug(folder.FullPath)
		}
		folder.LastSynced = time.Now()

		found := false
		for i := range s.data.Folders {
			if s.data.Folders[i].DriveID == folder.DriveID {
				s.data.Folders[i] = folder
				found = true
				break
			}
		}
		if !found {
			s.data.Folders = append(s.data.Folders, folder)
		}
	}

	return s.save()
}

// BulkUpsertClips inserts or updates multiple clips at once
func (s *StockDB) BulkUpsertClips(clips []StockClipEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, clip := range clips {
		found := false
		for i := range s.data.Clips {
			if s.data.Clips[i].ClipID == clip.ClipID {
				s.data.Clips[i] = clip
				found = true
				break
			}
		}
		if !found {
			s.data.Clips = append(s.data.Clips, clip)
		}
	}

	return s.save()
}
