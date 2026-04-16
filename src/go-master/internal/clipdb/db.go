// Package clipdb provides JSON-backed clip database management.
// Separate from StockDB to handle clips from different sources.
package clipdb

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

type ClipDB struct {
	dbPath string
	mu     sync.RWMutex
	data   *ClipDatabase
}

type ClipDatabase struct {
	LastSynced time.Time    `json:"last_synced"`
	Folders    []ClipFolder `json:"folders"`
	Clips      []ClipEntry  `json:"clips"`
}

type ClipFolder struct {
	TopicSlug  string    `json:"topic_slug"`
	DriveID    string    `json:"drive_id"`
	ParentID   string    `json:"parent_id"`
	FullPath   string    `json:"full_path"`
	LastSynced time.Time `json:"last_synced"`
}

type ClipEntry struct {
	ClipID    string `json:"clip_id"`
	FolderID  string `json:"folder_id"`
	Filename  string `json:"filename"`
	Source    string `json:"source"` // "youtube", "tiktok", "artlist"
	Tags      string `json:"tags"`
	Duration  int    `json:"duration"`
	DriveURL  string `json:"drive_url,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
}

func Open(dbPath string) (*ClipDB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	s := &ClipDB{
		dbPath: dbPath,
		data:   &ClipDatabase{},
	}

	if err := s.load(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load clip db: %w", err)
		}
		logger.Info("Creating new clip database", zap.String("path", dbPath))
		s.data = &ClipDatabase{}
		s.data.Folders = []ClipFolder{}
		s.data.Clips = []ClipEntry{}
		if err := s.save(); err != nil {
			return nil, fmt.Errorf("failed to create clip db: %w", err)
		}
	}

	return s, nil
}

func (s *ClipDB) load() error {
	data, err := os.ReadFile(s.dbPath)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, s.data); err != nil {
		return fmt.Errorf("failed to parse clip db: %w", err)
	}

	return nil
}

func (s *ClipDB) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal clip db: %w", err)
	}

	if err := os.WriteFile(s.dbPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write clip db: %w", err)
	}

	return nil
}

func (s *ClipDB) BulkUpsertClips(clips []ClipEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing := make(map[string]int)
	for i, c := range s.data.Clips {
		existing[c.ClipID] = i
	}

	for _, clip := range clips {
		if idx, ok := existing[clip.ClipID]; ok {
			s.data.Clips[idx] = clip
		} else {
			s.data.Clips = append(s.data.Clips, clip)
		}
	}

	s.data.LastSynced = time.Now()
	return s.save()
}

func (s *ClipDB) BulkUpsertFolders(folders []ClipFolder) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing := make(map[string]int)
	for i, f := range s.data.Folders {
		existing[f.TopicSlug] = i
	}

	for _, folder := range folders {
		if idx, ok := existing[folder.TopicSlug]; ok {
			s.data.Folders[idx] = folder
		} else {
			s.data.Folders = append(s.data.Folders, folder)
		}
	}

	return s.save()
}

func (s *ClipDB) SearchClipsByTags(tags []string) ([]ClipEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []ClipEntry

	for _, clip := range s.data.Clips {
		for _, tag := range tags {
			if containsTag(clip.Tags, tag) {
				results = append(results, clip)
				break
			}
		}
	}

	return results, nil
}

func (s *ClipDB) GetClipsByFolder(folderID string) ([]ClipEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []ClipEntry
	for _, clip := range s.data.Clips {
		if clip.FolderID == folderID {
			results = append(results, clip)
		}
	}

	return results, nil
}

func (s *ClipDB) GetAllClips() []ClipEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clips := make([]ClipEntry, len(s.data.Clips))
	copy(clips, s.data.Clips)
	return clips
}

func (s *ClipDB) GetClipCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data.Clips)
}

func (s *ClipDB) GetLastSyncTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.LastSynced
}

func (s *ClipDB) ClipExists(videoID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, clip := range s.data.Clips {
		if clip.ClipID == videoID {
			return true, nil
		}
	}
	return false, nil
}

func (s *ClipDB) AddClip(record *ClipEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.Clips = append(s.data.Clips, *record)
	return s.save()
}

func (s *ClipDB) GetClip(videoID string) (*ClipEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, clip := range s.data.Clips {
		if clip.ClipID == videoID {
			return &clip, nil
		}
	}
	return nil, nil
}

func (s *ClipDB) UpdateClip(record *ClipEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, clip := range s.data.Clips {
		if clip.ClipID == record.ClipID {
			s.data.Clips[i] = *record
			return s.save()
		}
	}
	return nil
}

func containsTag(clipTags, searchTag string) bool {
	lowerTags := toLowerWords(clipTags)
	lowerSearch := strings.ToLower(searchTag)
	for _, t := range lowerTags {
		if t == lowerSearch {
			return true
		}
	}
	return false
}

func toLowerWords(s string) []string {
	var result []string
	word := ""
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			word += string(c)
		} else if word != "" {
			result = append(result, strings.ToLower(word))
			word = ""
		}
	}
	if word != "" {
		result = append(result, strings.ToLower(word))
	}
	return result
}
