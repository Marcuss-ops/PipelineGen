// Package artlistdb provides hot-reloadable Artlist clip index management.
// ArtlistClip type is defined in db.go
package artlistdb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Index holds all Artlist clips available for association.
type Index struct {
	FolderID  string                   `json:"folder_id"`
	Clips     []ArtlistClip            `json:"clips"`
	CreatedAt string                   `json:"created_at,omitempty"`
	ByTerm    map[string][]ArtlistClip `json:"-"`
}

// Manager provides hot-reloadable Artlist index.
type Manager struct {
	path            string
	index           *Index
	mu              sync.RWMutex
	lastModified    time.Time
	lastSize        int64
	refreshInterval time.Duration
	logger          *zap.Logger
	stopCh          chan struct{}
}

// NewManager creates a new Artlist index manager.
func NewManager(path string, logger *zap.Logger) (*Manager, error) {
	m := &Manager{
		path:            path,
		refreshInterval: 5 * time.Minute, // Default: check every 5 minutes
		logger:          logger,
		stopCh:          make(chan struct{}),
	}

	// Initial load
	if err := m.load(); err != nil {
		return nil, err
	}

	return m, nil
}

// SetRefreshInterval changes the auto-refresh interval.
func (m *Manager) SetRefreshInterval(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshInterval = d
}

// Start begins the auto-refresh goroutine.
func (m *Manager) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(m.refreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := m.RefreshIfChanged(); err != nil {
					m.logger.Warn("Failed to refresh Artlist index", zap.Error(err))
				}
			case <-ctx.Done():
				return
			case <-m.stopCh:
				return
			}
		}
	}()

	m.logger.Info("Artlist index manager started",
		zap.String("path", m.path),
		zap.Duration("refresh_interval", m.refreshInterval),
	)
}

// Stop halts the auto-refresh goroutine.
func (m *Manager) Stop() {
	close(m.stopCh)
}

// RefreshIfChanged reloads the index only if the file has changed.
func (m *Manager) RefreshIfChanged() error {
	info, err := os.Stat(m.path)
	if err != nil {
		return fmt.Errorf("failed to stat Artlist index: %w", err)
	}

	m.mu.RLock()
	currentModTime := m.lastModified
	currentSize := m.lastSize
	m.mu.RUnlock()

	// Check if file changed
	if info.ModTime().Equal(currentModTime) && info.Size() == currentSize {
		return nil // No change
	}

	// File changed, reload
	m.logger.Info("Artlist index file changed, reloading",
		zap.Time("old_mtime", currentModTime),
		zap.Time("new_mtime", info.ModTime()),
		zap.Int64("old_size", currentSize),
		zap.Int64("new_size", info.Size()),
	)

	return m.load()
}

// ForceRefresh reloads the index regardless of file changes.
func (m *Manager) ForceRefresh() error {
	return m.load()
}

// load reads the index file and rebuilds the ByTerm map.
func (m *Manager) load() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("failed to read Artlist index: %w", err)
	}

	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return fmt.Errorf("failed to parse Artlist index: %w", err)
	}

	// Build ByTerm map
	idx.ByTerm = make(map[string][]ArtlistClip)
	for _, clip := range idx.Clips {
		idx.ByTerm[clip.Term] = append(idx.ByTerm[clip.Term], clip)
	}

	// Get file info for change detection
	info, err := os.Stat(m.path)
	if err != nil {
		return fmt.Errorf("failed to stat Artlist index: %w", err)
	}

	// Update with lock
	m.mu.Lock()
	m.index = &idx
	m.lastModified = info.ModTime()
	m.lastSize = info.Size()
	m.mu.Unlock()

	termCount := len(idx.ByTerm)
	clipCount := len(idx.Clips)

	m.logger.Info("Artlist index loaded",
		zap.String("path", m.path),
		zap.Int("terms", termCount),
		zap.Int("clips", clipCount),
	)

	return nil
}

// GetIndex returns the current index (read-only).
func (m *Manager) GetIndex() *Index {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.index
}

// GetClipsByTerm returns clips for a specific term.
func (m *Manager) GetClipsByTerm(term string) []ArtlistClip {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.index == nil || m.index.ByTerm == nil {
		return nil
	}

	return m.index.ByTerm[term]
}

// GetAllTerms returns all available terms.
func (m *Manager) GetAllTerms() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.index == nil || m.index.ByTerm == nil {
		return nil
	}

	terms := make([]string, 0, len(m.index.ByTerm))
	for term := range m.index.ByTerm {
		terms = append(terms, term)
	}
	return terms
}

// ManagerStats holds typed index manager statistics.
type ManagerStats struct {
	Loaded          bool      `json:"loaded"`
	Path            string    `json:"path,omitempty"`
	Terms           int       `json:"terms,omitempty"`
	Clips           int       `json:"clips,omitempty"`
	LastModified    time.Time `json:"last_modified,omitempty"`
	RefreshInterval string    `json:"refresh_interval,omitempty"`
}

// GetStats returns index statistics.
func (m *Manager) GetStats() ManagerStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.index == nil {
		return ManagerStats{Loaded: false}
	}

	return ManagerStats{
		Loaded:          true,
		Path:            m.path,
		Terms:           len(m.index.ByTerm),
		Clips:           len(m.index.Clips),
		LastModified:    m.lastModified,
		RefreshInterval: m.refreshInterval.String(),
	}
}

// LegacyLoadArtlistIndex loads the Artlist clip index from JSON file (backwards compat).
// Deprecated: Use NewManager instead for hot-reload support.
func LegacyLoadArtlistIndex(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read Artlist index: %w", err)
	}

	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("failed to parse Artlist index: %w", err)
	}

	idx.ByTerm = make(map[string][]ArtlistClip)
	for _, clip := range idx.Clips {
		idx.ByTerm[clip.Term] = append(idx.ByTerm[clip.Term], clip)
	}

	return &idx, nil
}
