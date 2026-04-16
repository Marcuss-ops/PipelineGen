// Package artlist provides hot-reload for Artlist clip index.
package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// ArtlistIndexWatcher monitors the Artlist index file for changes
// and provides hot-reload capability.
type ArtlistIndexWatcher struct {
	indexPath    string
	index        *scriptdocs.ArtlistIndex
	mu           sync.RWMutex
	refreshTTL   time.Duration
	lastModified time.Time
	stopCh       chan struct{}
	lastError    error
}

// NewArtlistIndexWatcher creates a new watcher with periodic refresh
func NewArtlistIndexWatcher(indexPath string, refreshInterval time.Duration) (*ArtlistIndexWatcher, error) {
	w := &ArtlistIndexWatcher{
		indexPath:  indexPath,
		refreshTTL: refreshInterval,
		stopCh:     make(chan struct{}),
	}

	// Initial load
	if err := w.Reload(); err != nil {
		return nil, fmt.Errorf("failed to load initial index: %w", err)
	}

	return w, nil
}

// Reload reloads the index from file
func (w *ArtlistIndexWatcher) Reload() error {
	info, err := os.Stat(w.indexPath)
	if err != nil {
		w.mu.Lock()
		w.lastError = fmt.Errorf("stat index file: %w", err)
		w.mu.Unlock()
		return w.lastError
	}

	// Check if file was modified
	w.mu.RLock()
	if !info.ModTime().After(w.lastModified) && w.index != nil {
		w.mu.RUnlock()
		return nil // No changes
	}
	w.mu.RUnlock()

	// Load new index
	data, err := os.ReadFile(w.indexPath)
	if err != nil {
		w.mu.Lock()
		w.lastError = fmt.Errorf("read index file: %w", err)
		w.mu.Unlock()
		return w.lastError
	}

	var idx scriptdocs.ArtlistIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		w.mu.Lock()
		w.lastError = fmt.Errorf("parse index: %w", err)
		w.mu.Unlock()
		return w.lastError
	}

	// Build ByTerm map
	idx.ByTerm = make(map[string][]scriptdocs.ArtlistClip)
	for _, clip := range idx.Clips {
		idx.ByTerm[clip.Term] = append(idx.ByTerm[clip.Term], clip)
	}

	// Update cached index
	w.mu.Lock()
	w.index = &idx
	w.lastModified = info.ModTime()
	w.lastError = nil
	w.mu.Unlock()

	logger.Info("Artlist index reloaded",
		zap.Int("clips", len(idx.Clips)),
		zap.Int("terms", len(idx.ByTerm)),
	)

	return nil
}

// GetIndex returns the current index (thread-safe)
func (w *ArtlistIndexWatcher) GetIndex() (*scriptdocs.ArtlistIndex, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.index == nil {
		return nil, fmt.Errorf("index not loaded")
	}

	return w.index, nil
}

// GetClipByTerm returns clips for a specific term
func (w *ArtlistIndexWatcher) GetClipByTerm(term string) []scriptdocs.ArtlistClip {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.index == nil || w.index.ByTerm == nil {
		return nil
	}

	return w.index.ByTerm[term]
}

// WatcherStats holds typed index watcher statistics.
type WatcherStats struct {
	IndexPath    string    `json:"index_path"`
	LastModified time.Time `json:"last_modified"`
	Loaded       bool      `json:"loaded"`
	LastError    string    `json:"last_error"`
	TotalClips   int       `json:"total_clips,omitempty"`
	TotalTerms   int       `json:"total_terms,omitempty"`
	CreatedAt    string    `json:"created_at,omitempty"`
}

// GetStats returns index statistics
func (w *ArtlistIndexWatcher) GetStats() WatcherStats {
	w.mu.RLock()
	defer w.mu.RUnlock()

	stats := WatcherStats{
		IndexPath:    w.indexPath,
		LastModified: w.lastModified,
		Loaded:       w.index != nil,
	}

	if w.lastError != nil {
		stats.LastError = w.lastError.Error()
	}

	if w.index != nil {
		stats.TotalClips = len(w.index.Clips)
		stats.TotalTerms = len(w.index.ByTerm)
		stats.CreatedAt = w.index.CreatedAt
	}

	return stats
}

// StartAutoRefresh starts periodic index refresh
func (w *ArtlistIndexWatcher) StartAutoRefresh(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(w.refreshTTL)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := w.Reload(); err != nil {
					logger.Warn("Failed to refresh Artlist index",
						zap.Error(err),
					)
				}
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			}
		}
	}()

	logger.Info("Artlist index auto-refresh started",
		zap.Duration("interval", w.refreshTTL),
	)
}

// Stop stops the auto-refresh
func (w *ArtlistIndexWatcher) Stop() {
	close(w.stopCh)
	logger.Info("Artlist index auto-refresh stopped")
}

// ForceReload forces immediate reload regardless of modification time
func (w *ArtlistIndexWatcher) ForceReload() error {
	w.mu.Lock()
	w.lastModified = time.Time{} // Force reload
	w.mu.Unlock()

	return w.Reload()
}

// GetLastModified returns the last modification time of the index file
func (w *ArtlistIndexWatcher) GetLastModified() time.Time {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastModified
}

// HasError returns true if last reload failed
func (w *ArtlistIndexWatcher) HasError() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastError != nil
}

// GetLastError returns the last reload error
func (w *ArtlistIndexWatcher) GetLastError() error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastError
}
