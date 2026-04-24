// Package clipdb provides JSON-backed clip database management.
// Separate from StockDB to handle clips from different sources.
package clipdb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	ClipID    string   `json:"clip_id"`
	FolderID  string   `json:"folder_id"`
	Filename  string   `json:"filename"`
	Source    string   `json:"source"` // "youtube", "tiktok", "artlist"
	Tags      []string `json:"tags"`
	Duration  int      `json:"duration"`
	DriveURL  string   `json:"drive_url,omitempty"`
	LocalPath string   `json:"local_path,omitempty"`
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

// SearchFolders returns folders ordered by relevance to the query.
func (s *ClipDB) SearchFolders(query string) []ClipFolder {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data == nil || len(s.data.Folders) == 0 {
		return []ClipFolder{}
	}

	query = strings.TrimSpace(query)
	queryLower := strings.ToLower(query)
	querySlug := folderQuerySlug(query)

	type scoredFolder struct {
		folder ClipFolder
		score  int
		depth  int
	}

	results := make([]scoredFolder, 0, len(s.data.Folders))
	for _, folder := range s.data.Folders {
		path := strings.TrimSpace(folder.FullPath)
		slug := strings.TrimSpace(folder.TopicSlug)
		nameLower := strings.ToLower(path)
		slugLower := strings.ToLower(slug)
		pathSlug := folderQuerySlug(path)
		score := 0

		switch {
		case querySlug != "" && slugLower == querySlug:
			score = 100
		case queryLower != "" && strings.EqualFold(path, query):
			score = 96
		case querySlug != "" && strings.HasSuffix(pathSlug, "/"+querySlug):
			score = 92
		case querySlug != "" && strings.Contains(pathSlug, querySlug):
			score = 80
		case queryLower != "" && strings.Contains(nameLower, queryLower):
			score = 60
		}

		if score == 0 {
			continue
		}
		depth := strings.Count(path, "/") + 1
		results = append(results, scoredFolder{folder: folder, score: score, depth: depth})
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		if results[i].depth != results[j].depth {
			return results[i].depth < results[j].depth
		}
		return strings.ToLower(results[i].folder.FullPath) < strings.ToLower(results[j].folder.FullPath)
	})

	out := make([]ClipFolder, 0, len(results))
	for _, r := range results {
		out = append(out, r.folder)
	}
	return out
}

func (s *ClipDB) GetAllClips() []ClipEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clips := make([]ClipEntry, len(s.data.Clips))
	copy(clips, s.data.Clips)
	return clips
}

// GetAllFolders returns a copy of all folder records.
func (s *ClipDB) GetAllFolders() []ClipFolder {
	s.mu.RLock()
	defer s.mu.RUnlock()

	folders := make([]ClipFolder, len(s.data.Folders))
	copy(folders, s.data.Folders)
	return folders
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

// Close is a no-op for JSON backend
func (s *ClipDB) Close() error {
	return s.save()
}

func containsTag(clipTags []string, searchTag string) bool {
	lowerSearch := strings.ToLower(searchTag)
	for _, t := range clipTags {
		if strings.ToLower(t) == lowerSearch {
			return true
		}
	}
	return false
}

func folderQuerySlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
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

// DeleteClipByID removes one clip entry by Drive file ID.
func (s *ClipDB) DeleteClipByID(clipID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(clipID) == "" {
		return nil
	}
	out := make([]ClipEntry, 0, len(s.data.Clips))
	for _, c := range s.data.Clips {
		if c.ClipID == clipID {
			continue
		}
		out = append(out, c)
	}
	s.data.Clips = out
	s.data.LastSynced = time.Now()
	return s.save()
}

// DeleteClipsByIDs removes multiple clip entries by Drive file ID.
func (s *ClipDB) DeleteClipsByIDs(clipIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(clipIDs) == 0 {
		return nil
	}
	rm := make(map[string]bool, len(clipIDs))
	for _, id := range clipIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			rm[id] = true
		}
	}
	if len(rm) == 0 {
		return nil
	}
	out := make([]ClipEntry, 0, len(s.data.Clips))
	for _, c := range s.data.Clips {
		if rm[c.ClipID] {
			continue
		}
		out = append(out, c)
	}
	s.data.Clips = out
	s.data.LastSynced = time.Now()
	return s.save()
}

// DeduplicateByFolderAndFilename removes duplicate clip entries with same (folder_id, filename).
// It keeps one deterministic canonical entry and returns duplicate clip IDs removed from DB.
func (s *ClipDB) DeduplicateByFolderAndFilename() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	type key struct {
		folder string
		name   string
	}
	type entryRef struct {
		idx    int
		clipID string
	}

	byKey := make(map[key][]entryRef)
	for i, c := range s.data.Clips {
		k := key{
			folder: strings.TrimSpace(strings.ToLower(c.FolderID)),
			name:   strings.TrimSpace(strings.ToLower(c.Filename)),
		}
		if k.folder == "" || k.name == "" {
			continue
		}
		byKey[k] = append(byKey[k], entryRef{idx: i, clipID: strings.TrimSpace(c.ClipID)})
	}

	keepIndex := make(map[int]bool, len(s.data.Clips))
	for i := range s.data.Clips {
		keepIndex[i] = true
	}
	removedIDs := make([]string, 0)
	for _, refs := range byKey {
		if len(refs) <= 1 {
			continue
		}
		sort.SliceStable(refs, func(i, j int) bool {
			return refs[i].clipID < refs[j].clipID
		})
		for i := 1; i < len(refs); i++ {
			keepIndex[refs[i].idx] = false
			if refs[i].clipID != "" {
				removedIDs = append(removedIDs, refs[i].clipID)
			}
		}
	}
	if len(removedIDs) == 0 {
		return nil, nil
	}

	out := make([]ClipEntry, 0, len(s.data.Clips)-len(removedIDs))
	for i, c := range s.data.Clips {
		if !keepIndex[i] {
			continue
		}
		out = append(out, c)
	}
	s.data.Clips = out
	s.data.LastSynced = time.Now()
	if err := s.save(); err != nil {
		return nil, err
	}
	sort.Strings(removedIDs)
	return removedIDs, nil
}
