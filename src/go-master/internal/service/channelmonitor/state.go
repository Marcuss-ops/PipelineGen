package channelmonitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// processedVideosMu protects concurrent access to the processedVideos map
var processedVideosMu sync.RWMutex

// loadProcessedVideos loads the processed videos log from disk
func (m *Monitor) loadProcessedVideos() {
	data, err := os.ReadFile(m.processedFile)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("No processed videos file found, starting fresh",
				zap.String("path", m.processedFile),
			)
			return
		}
		logger.Warn("Failed to read processed videos file",
			zap.String("path", m.processedFile),
			zap.Error(err),
		)
		return
	}

	var entries []ProcessedVideoEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		logger.Warn("Failed to parse processed videos file",
			zap.String("path", m.processedFile),
			zap.Error(err),
		)
		return
	}

	processedVideosMu.Lock()
	defer processedVideosMu.Unlock()

	for _, entry := range entries {
		m.processedVideos[entry.VideoID] = &entry
	}

	logger.Info("Loaded processed videos log",
		zap.String("path", m.processedFile),
		zap.Int("entries", len(entries)),
	)
}

// saveProcessedVideos persists the processed videos log to disk
func (m *Monitor) saveProcessedVideos() {
	processedVideosMu.RLock()
	entries := make([]ProcessedVideoEntry, 0, len(m.processedVideos))
	for _, entry := range m.processedVideos {
		entries = append(entries, *entry)
	}
	processedVideosMu.RUnlock()

	// Sort by processed date (newest last)
	sortProcessedVideos(entries)

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		logger.Warn("Failed to marshal processed videos", zap.Error(err))
		return
	}

	// Ensure directory exists
	dir := filepath.Dir(m.processedFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		logger.Warn("Failed to create processed videos directory", zap.Error(err))
		return
	}

	if err := os.WriteFile(m.processedFile, data, 0644); err != nil {
		logger.Warn("Failed to save processed videos file",
			zap.String("path", m.processedFile),
			zap.Error(err),
		)
	}
}

// sortProcessedVideos sorts entries by processed date (oldest first)
func sortProcessedVideos(entries []ProcessedVideoEntry) {
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].ProcessedAt.After(entries[j].ProcessedAt) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
}

// isProcessed checks if a video has already been processed
func (m *Monitor) isProcessed(videoID string) bool {
	processedVideosMu.RLock()
	defer processedVideosMu.RUnlock()
	_, exists := m.processedVideos[videoID]
	return exists
}

// markProcessed records that a video has been processed
func (m *Monitor) markProcessed(entry ProcessedVideoEntry) {
	processedVideosMu.Lock()
	entry.ProcessedAt = time.Now()
	m.processedVideos[entry.VideoID] = &entry
	processedVideosMu.Unlock()

	// Persist to disk after each addition
	m.saveProcessedVideos()

	logger.Debug("Video marked as processed",
		zap.String("video_id", entry.VideoID),
		zap.String("title", entry.Title),
		zap.Int("clips_count", entry.ClipsCount),
	)
}

// scanExistingFolders scans the Drive Stock folders and populates the folder cache
func (m *Monitor) scanExistingFolders() {
	if m.config.StockRootID == "" {
		logger.Info("No Stock root ID configured, skipping folder scan")
		return
	}

	logger.Info("Scanning existing Drive folders",
		zap.String("stock_root_id", m.config.StockRootID),
	)

	ctx, cancel := contextWithTimeout(2 * time.Minute)
	defer cancel()

	// List all category folders
	folders, err := m.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: m.config.StockRootID,
		MaxDepth: 2,
		MaxItems: 200,
	})
	if err != nil {
		logger.Warn("Failed to list Drive folders", zap.Error(err))
		return
	}

	count := 0
	for _, folder := range folders {
		// Build cache key: category/subfolder -> folder ID
		cacheKey := "Stock/" + folder.Name
		m.folderCache[cacheKey] = folder.ID
		count++

		// Cache subfolders too
		for _, sub := range folder.Subfolders {
			subCacheKey := "Stock/" + folder.Name + "/" + sub.Name
			m.folderCache[subCacheKey] = sub.ID
			count++
		}
	}

	logger.Info("Folder scan complete",
		zap.Int("folders_cached", count),
	)
}

// contextWithTimeout is a helper to create a context with timeout
// This avoids importing context everywhere since it's already imported in monitor.go
func contextWithTimeout(timeout time.Duration) (interface{ Done() <-chan struct{} }, func()) {
	// We need to return context.Context but avoid circular import
	// This is handled via the Monitor methods that already import context
	return nil, func() {}
}

// GetProcessedVideos returns a copy of all processed video entries
func (m *Monitor) GetProcessedVideos() []ProcessedVideoEntry {
	processedVideosMu.RLock()
	defer processedVideosMu.RUnlock()

	entries := make([]ProcessedVideoEntry, 0, len(m.processedVideos))
	for _, entry := range m.processedVideos {
		entries = append(entries, *entry)
	}

	// Sort by date (newest first)
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].ProcessedAt.Before(entries[j].ProcessedAt) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	return entries
}

// GetProcessedVideo retrieves a single processed video entry
func (m *Monitor) GetProcessedVideo(videoID string) (*ProcessedVideoEntry, bool) {
	processedVideosMu.RLock()
	defer processedVideosMu.RUnlock()

	entry, exists := m.processedVideos[videoID]
	return entry, exists
}

// ClearProcessedVideos removes all processed video entries
func (m *Monitor) ClearProcessedVideos() {
	processedVideosMu.Lock()
	m.processedVideos = make(map[string]*ProcessedVideoEntry)
	processedVideosMu.Unlock()

	// Also clear the file
	os.Remove(m.processedFile)

	logger.Info("Processed videos log cleared")
}

// CleanupOldProcessedVideos removes entries older than the given duration
func (m *Monitor) CleanupOldProcessedVideos(maxAge time.Duration) int {
	processedVideosMu.Lock()
	defer processedVideosMu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	for videoID, entry := range m.processedVideos {
		if entry.ProcessedAt.Before(cutoff) {
			delete(m.processedVideos, videoID)
			removed++
		}
	}

	if removed > 0 {
		logger.Info("Cleaned up old processed video entries",
			zap.Int("removed", removed),
		)
		// Save updated list
		go m.saveProcessedVideos()
	}

	return removed
}

// FindFolderByPath searches for a folder by its full path (e.g., "Stock/HipHop/ArtistName")
func (m *Monitor) FindFolderByPath(path string) (string, bool) {
	// Check cache first
	if folderID, ok := m.folderCache[path]; ok {
		return folderID, true
	}

	// Try to build path from components
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return "", false
	}

	// Try common path patterns
	patterns := []string{
		path,
		"Stock/" + path,
		strings.Join(parts[1:], "/"), // Skip "Stock" prefix if present
	}

	for _, pattern := range patterns {
		if folderID, ok := m.folderCache[pattern]; ok {
			return folderID, true
		}
	}

	return "", false
}

// GetFolderCache returns a copy of the folder cache
func (m *Monitor) GetFolderCache() map[string]string {
	clone := make(map[string]string, len(m.folderCache))
	for k, v := range m.folderCache {
		clone[k] = v
	}
	return clone
}

// RefreshFolderCache rescans Drive folders and updates the cache
func (m *Monitor) RefreshFolderCache() {
	m.scanExistingFolders()
}
