package artlist

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"velox/go-master/internal/service/scriptdocs"
)

func createTestIndex(t *testing.T, path string) {
	index := scriptdocs.ArtlistIndex{
		FolderID:  "test_folder",
		CreatedAt: time.Now().Format(time.RFC3339),
		Clips: []scriptdocs.ArtlistClip{
			{Name: "people_01.mp4", Term: "people", URL: "https://example.com/people_01"},
			{Name: "city_01.mp4", Term: "city", URL: "https://example.com/city_01"},
			{Name: "technology_01.mp4", Term: "technology", URL: "https://example.com/tech_01"},
		},
	}

	data, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("Failed to marshal test index: %v", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("Failed to write test index: %v", err)
	}
}

func TestNewArtlistIndexWatcher(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	createTestIndex(t, path)

	watcher, err := NewArtlistIndexWatcher(path, 1*time.Hour)
	if err != nil {
		t.Fatalf("NewArtlistIndexWatcher() error = %v", err)
	}
	defer watcher.Stop()

	if watcher == nil {
		t.Fatal("NewArtlistIndexWatcher() returned nil")
	}

	if watcher.indexPath != path {
		t.Errorf("Expected index path %s, got %s", path, watcher.indexPath)
	}
}

func TestArtlistIndexWatcherReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	createTestIndex(t, path)

	watcher, err := NewArtlistIndexWatcher(path, 1*time.Hour)
	if err != nil {
		t.Fatalf("NewArtlistIndexWatcher() error = %v", err)
	}
	defer watcher.Stop()

	// Get initial index
	idx, err := watcher.GetIndex()
	if err != nil {
		t.Fatalf("GetIndex() error = %v", err)
	}

	if len(idx.Clips) != 3 {
		t.Errorf("Expected 3 clips, got %d", len(idx.Clips))
	}

	if len(idx.ByTerm) != 3 {
		t.Errorf("Expected 3 terms, got %d", len(idx.ByTerm))
	}
}

func TestArtlistIndexWatcherGetClipByTerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	createTestIndex(t, path)

	watcher, err := NewArtlistIndexWatcher(path, 1*time.Hour)
	if err != nil {
		t.Fatalf("NewArtlistIndexWatcher() error = %v", err)
	}
	defer watcher.Stop()

	clips := watcher.GetClipByTerm("people")
	if len(clips) != 1 {
		t.Errorf("Expected 1 clip for 'people', got %d", len(clips))
	}

	if clips[0].Name != "people_01.mp4" {
		t.Errorf("Expected clip name 'people_01.mp4', got %s", clips[0].Name)
	}

	// Test non-existent term
	clips = watcher.GetClipByTerm("nonexistent")
	if clips != nil {
		t.Errorf("Expected nil for non-existent term, got %d clips", len(clips))
	}
}

func TestArtlistIndexWatcherGetStats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	createTestIndex(t, path)

	watcher, err := NewArtlistIndexWatcher(path, 1*time.Hour)
	if err != nil {
		t.Fatalf("NewArtlistIndexWatcher() error = %v", err)
	}
	defer watcher.Stop()

	stats := watcher.GetStats()

	if stats["loaded"] != true {
		t.Error("Expected index to be loaded")
	}

	if stats["total_clips"] != 3 {
		t.Errorf("Expected 3 clips in stats, got %v", stats["total_clips"])
	}

	if stats["total_terms"] != 3 {
		t.Errorf("Expected 3 terms in stats, got %v", stats["total_terms"])
	}
}

func TestArtlistIndexWatcherForceReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	createTestIndex(t, path)

	watcher, err := NewArtlistIndexWatcher(path, 1*time.Hour)
	if err != nil {
		t.Fatalf("NewArtlistIndexWatcher() error = %v", err)
	}
	defer watcher.Stop()

	// Force reload
	if err := watcher.ForceReload(); err != nil {
		t.Errorf("ForceReload() error = %v", err)
	}

	// Verify index still valid
	idx, err := watcher.GetIndex()
	if err != nil {
		t.Errorf("GetIndex() after ForceReload() error = %v", err)
	}

	if len(idx.Clips) != 3 {
		t.Errorf("Expected 3 clips after ForceReload, got %d", len(idx.Clips))
	}
}

func TestArtlistIndexWatcherFileModification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	createTestIndex(t, path)

	watcher, err := NewArtlistIndexWatcher(path, 1*time.Hour)
	if err != nil {
		t.Fatalf("NewArtlistIndexWatcher() error = %v", err)
	}
	defer watcher.Stop()

	// Wait a bit to ensure file modification time difference
	time.Sleep(100 * time.Millisecond)

	// Modify the index file
	index := scriptdocs.ArtlistIndex{
		FolderID:  "updated_folder",
		CreatedAt: time.Now().Format(time.RFC3339),
		Clips: []scriptdocs.ArtlistClip{
			{Name: "new_clip_01.mp4", Term: "people", URL: "https://example.com/new_01"},
			{Name: "new_clip_02.mp4", Term: "people", URL: "https://example.com/new_02"},
			{Name: "new_clip_03.mp4", Term: "city", URL: "https://example.com/new_03"},
			{Name: "new_clip_04.mp4", Term: "technology", URL: "https://example.com/new_04"},
			{Name: "new_clip_05.mp4", Term: "nature", URL: "https://example.com/new_05"},
		},
	}

	data, _ := json.Marshal(index)
	os.WriteFile(path, data, 0644)

	// Force reload should detect changes
	if err := watcher.ForceReload(); err != nil {
		t.Fatalf("ForceReload() error = %v", err)
	}

	idx, _ := watcher.GetIndex()
	if len(idx.Clips) != 5 {
		t.Errorf("Expected 5 clips after update, got %d", len(idx.Clips))
	}

	if len(idx.ByTerm) != 4 {
		t.Errorf("Expected 4 terms after update, got %d", len(idx.ByTerm))
	}
}

func TestArtlistIndexWatcherHasError(t *testing.T) {
	watcher := &ArtlistIndexWatcher{
		indexPath: "/nonexistent/path",
	}

	// Try to reload (should fail)
	err := watcher.Reload()
	if err == nil {
		t.Error("Expected error for nonexistent path")
	}

	if !watcher.HasError() {
		t.Error("Expected HasError() to return true")
	}

	if watcher.GetLastError() == nil {
		t.Error("Expected GetLastError() to return error")
	}
}

func TestArtlistIndexWatcherGetLastModified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	createTestIndex(t, path)

	watcher, err := NewArtlistIndexWatcher(path, 1*time.Hour)
	if err != nil {
		t.Fatalf("NewArtlistIndexWatcher() error = %v", err)
	}
	defer watcher.Stop()

	modTime := watcher.GetLastModified()
	if modTime.IsZero() {
		t.Error("Expected non-zero last modified time")
	}
}

func TestArtlistIndexWatcherAutoRefresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	createTestIndex(t, path)

	// Create watcher with very short refresh interval
	watcher, err := NewArtlistIndexWatcher(path, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("NewArtlistIndexWatcher() error = %v", err)
	}
	defer watcher.Stop()

	// Start auto refresh
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher.StartAutoRefresh(ctx)

	// Wait for at least one refresh cycle
	time.Sleep(250 * time.Millisecond)

	// Verify index is still valid
	idx, err := watcher.GetIndex()
	if err != nil {
		t.Errorf("GetIndex() after auto-refresh error = %v", err)
	}

	if len(idx.Clips) != 3 {
		t.Errorf("Expected 3 clips after auto-refresh, got %d", len(idx.Clips))
	}
}
