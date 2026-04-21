// Package jsondb provides JSON file-based storage for VeloxEditing.
package jsondb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"velox/go-master/internal/clip"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

const clipIndexFile = "clip_index.json"

// ClipIndexStore manages clip index persistence
type ClipIndexStore struct {
	dataDir  string
	mu       sync.RWMutex
	index    *clip.ClipIndex
}

// NewClipIndexStore creates a new clip index store
func NewClipIndexStore(dataDir string) (*ClipIndexStore, error) {
	if dataDir == "" {
		dataDir = "./data"
	}

	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	store := &ClipIndexStore{
		dataDir: dataDir,
	}

	// Try to load existing index
	if err := store.loadIndex(); err != nil {
		logger.Warn("No existing clip index found, will create fresh", zap.Error(err))
	}

	return store, nil
}

// GetFilePath returns the full path to the clip index file
func (s *ClipIndexStore) GetFilePath() string {
	return filepath.Join(s.dataDir, clipIndexFile)
}

// SaveIndex saves the clip index to disk
func (s *ClipIndexStore) SaveIndex(index *clip.ClipIndex) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.GetFilePath()

	// Marshal index to JSON
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal clip index: %w", err)
	}

	// Write to temporary file first
	tempPath := filePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempPath, filePath); err != nil {
		// Clean up temp file on failure
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename file: %w", err)
	}

	// Update in-memory cache
	s.index = index

	logger.Info("Saved clip index to disk",
		zap.Int("clips", len(index.Clips)),
		zap.Int("folders", len(index.Folders)),
		zap.String("path", filePath))

	return nil
}

// LoadIndex loads the clip index from disk
func (s *ClipIndexStore) LoadIndex() (*clip.ClipIndex, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index != nil {
		return s.index, nil
	}

	if err := s.loadIndex(); err != nil {
		return nil, err
	}

	return s.index, nil
}

// loadIndex internal method to load index from disk
func (s *ClipIndexStore) loadIndex() error {
	filePath := s.GetFilePath()

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("clip index file not found: %s", filePath)
	}

	// Read file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read clip index file: %w", err)
	}

	// Unmarshal JSON
	var index clip.ClipIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("failed to parse clip index: %w", err)
	}

	// Validate version
	if index.Version != "1.0" {
		logger.Warn("Clip index version mismatch, may need migration",
			zap.String("found", index.Version),
			zap.String("expected", "1.0"))
	}

	// Update cache
	s.index = &index

	logger.Info("Loaded clip index from disk",
		zap.Int("clips", len(index.Clips)),
		zap.Int("folders", len(index.Folders)),
		zap.String("path", filePath))

	return nil
}

// GetIndex returns the cached index
func (s *ClipIndexStore) GetIndex() *clip.ClipIndex {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.index
}

// SetIndex sets the index in cache (without saving)
func (s *ClipIndexStore) SetIndex(index *clip.ClipIndex) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.index = index
}

// BackfillMediaTypes backfills the media_type field for clips that have it empty.
// This is needed for existing indices created before media_type was added.
func (s *ClipIndexStore) BackfillMediaTypes() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.index == nil {
		return 0, nil
	}

	backfilled := 0
	for i := range s.index.Clips {
		if s.index.Clips[i].MediaType == "" {
			s.index.Clips[i].MediaType = detectMediaTypeFromPath(s.index.Clips[i].FolderPath)
			backfilled++
		}
	}

	if backfilled > 0 {
		// Save updated index
		s.mu.Unlock()
		err := s.SaveIndex(s.index)
		s.mu.Lock()

		logger.Info("Backfilled media_type for clips",
			zap.Int("backfilled", backfilled),
			zap.Int("total", len(s.index.Clips)))

		return backfilled, err
	}

	return 0, nil
}

// detectMediaTypeFromPath determines media type from folder path (mirrors indexer logic)
func detectMediaTypeFromPath(path string) string {
	pathLower := strings.ToLower(path)
	segments := strings.Split(pathLower, "/")

	if len(segments) == 0 {
		return clip.MediaTypeClip
	}

	topFolder := strings.TrimSpace(segments[0])

	// Stock keywords
	stockKeywords := []string{"stock", "stock cartella", "stockfootage", "stock_footage", "stock-video", "stockvideo"}
	for _, kw := range stockKeywords {
		if strings.Contains(topFolder, kw) || topFolder == kw {
			return clip.MediaTypeStock
		}
	}

	// Clip keywords
	clipKeywords := []string{"clip", "clips", "cartellaclip", "clip_cartella", "clip-folder", "clipfolder"}
	for _, kw := range clipKeywords {
		if strings.Contains(topFolder, kw) || topFolder == kw {
			return clip.MediaTypeClip
		}
	}

	// Check if path contains clip keywords anywhere
	for _, kw := range clipKeywords {
		if strings.Contains(pathLower, kw) {
			return clip.MediaTypeClip
		}
	}

	// Default: check known clip groups
	clipGroups := []string{"boxe", "crimine", "discovery", "hiphop", "musica", "wwe", "tech", "stock",
		"interviews", "broll", "highlights", "general", "nature", "urban", "business", "voiceover"}
	for _, g := range clipGroups {
		if topFolder == g || strings.Contains(topFolder, g) {
			return clip.MediaTypeClip
		}
	}

	return clip.MediaTypeStock
}

// DeleteIndex deletes the clip index file
func (s *ClipIndexStore) DeleteIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.GetFilePath()

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil // Already deleted
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete clip index: %w", err)
	}

	s.index = nil

	logger.Info("Deleted clip index from disk", zap.String("path", filePath))
	return nil
}

// GetStats returns quick stats without loading full index
func (s *ClipIndexStore) GetStats() (*clip.IndexStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index != nil {
		return &s.index.Stats, nil
	}

	// Load from disk if not in cache
	index, err := s.LoadIndex()
	if err != nil {
		return nil, err
	}

	return &index.Stats, nil
}

// UpdateClipMetadata updates a single clip's metadata in the index
func (s *ClipIndexStore) UpdateClipMetadata(clipID string, updates func(*clip.IndexedClip)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load if not in cache
	if s.index == nil {
		if err := s.loadIndex(); err != nil {
			return err
		}
	}

	// Find and update clip
	for i, c := range s.index.Clips {
		if c.ID == clipID {
			updates(&s.index.Clips[i])
			
			// Save to disk
			return s.SaveIndex(s.index)
		}
	}

	return fmt.Errorf("clip not found: %s", clipID)
}

// RemoveClip removes a clip from the index
func (s *ClipIndexStore) RemoveClip(clipID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load if not in cache
	if s.index == nil {
		if err := s.loadIndex(); err != nil {
			return err
		}
	}

	// Find and remove clip
	initialLen := len(s.index.Clips)
	for i, c := range s.index.Clips {
		if c.ID == clipID {
			s.index.Clips = append(s.index.Clips[:i], s.index.Clips[i+1:]...)
			
			// Update stats
			s.index.Stats.TotalClips = len(s.index.Clips)
			if c.Group != "" {
				s.index.Stats.ClipsByGroup[c.Group]--
			}

			// Save to disk
			return s.SaveIndex(s.index)
		}
	}

	if initialLen == len(s.index.Clips) {
		return fmt.Errorf("clip not found: %s", clipID)
	}

	return nil
}
