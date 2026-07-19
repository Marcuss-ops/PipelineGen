// Package stockpipeline — source_cache_test.go (source cache unit tests).
//
// Tests the source cache key derivation, URL normalization, cache hit/miss
// logic, and invalidation paths. These are pure unit tests that do NOT
// require SQLite or a running server.
package stockpipeline

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/filesystem"
)

// testFS is the canonical LocalFSPort used across the source cache
// test suite. The production composition root injects the same
// filesystem.NewLocal() adapter — using it here exercises the typed
// port path end-to-end (PR-REFACTOR-P0-IO-BINDER pattern).
var testFS LocalFSPort = filesystem.NewLocal()

// ─────────────────────────────────────────────────────────────────────
// Test: DeriveSourceCacheKey determinism
// ─────────────────────────────────────────────────────────────────────

func TestDeriveSourceCacheKey_Deterministic(t *testing.T) {
	key1 := DeriveSourceCacheKey("https://www.youtube.com/watch?v=QdSbtEo3x_Y", "", "", false)
	key2 := DeriveSourceCacheKey("https://www.youtube.com/watch?v=QdSbtEo3x_Y", "", "", false)
	if key1 == "" {
		t.Fatal("DeriveSourceCacheKey returned empty key")
	}
	if key1 != key2 {
		t.Errorf("same inputs produced different keys: %q vs %q", key1, key2)
	}
}

func TestDeriveSourceCacheKey_DifferentURL_DifferentKey(t *testing.T) {
	key1 := DeriveSourceCacheKey("https://www.youtube.com/watch?v=AAAA1111", "", "", false)
	key2 := DeriveSourceCacheKey("https://www.youtube.com/watch?v=BBBB2222", "", "", false)
	if key1 == key2 {
		t.Errorf("different URLs produced same key: %q", key1)
	}
}

func TestDeriveSourceCacheKey_DifferentSection_DifferentKey(t *testing.T) {
	key1 := DeriveSourceCacheKey("https://www.youtube.com/watch?v=QdSbtEo3x_Y", "0:00-10:00", "", false)
	key2 := DeriveSourceCacheKey("https://www.youtube.com/watch?v=QdSbtEo3x_Y", "10:00-20:00", "", false)
	if key1 == key2 {
		t.Errorf("different sections produced same key: %q", key1)
	}
}

func TestDeriveSourceCacheKey_ForceKeyframes_DifferentKey(t *testing.T) {
	key1 := DeriveSourceCacheKey("https://www.youtube.com/watch?v=QdSbtEo3x_Y", "", "", false)
	key2 := DeriveSourceCacheKey("https://www.youtube.com/watch?v=QdSbtEo3x_Y", "", "", true)
	if key1 == key2 {
		t.Errorf("different force_keyframes produced same key: %q", key1)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test: normalizeSourceURL
// ─────────────────────────────────────────────────────────────────────

func TestNormalizeSourceURL_YouTube_WatchV(t *testing.T) {
	got := normalizeSourceURL("https://www.youtube.com/watch?v=QdSbtEo3x_Y&list=PLxyz")
	want := "https://www.youtube.com/watch?v=QdSbtEo3x_Y"
	if got != want {
		t.Errorf("normalizeSourceURL(%q) = %q, want %q", "watch?v+list", got, want)
	}
}

func TestNormalizeSourceURL_YouTube_BareID(t *testing.T) {
	got := normalizeSourceURL("https://youtu.be/QdSbtEo3x_Y")
	want := "https://www.youtube.com/watch?v=QdSbtEo3x_Y"
	if got != want {
		t.Errorf("normalizeSourceURL(%q) = %q, want %q", "youtu.be", got, want)
	}
}

func TestNormalizeSourceURL_NonYouTube_Unchanged(t *testing.T) {
	raw := "https://example.com/video.mp4"
	got := normalizeSourceURL(raw)
	if got != raw {
		t.Errorf("normalizeSourceURL(%q) = %q, want %q (non-YouTube should be unchanged)", raw, got, raw)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test: extractVideoIDFromURL
// ─────────────────────────────────────────────────────────────────────

func TestExtractVideoIDFromURL_WatchV(t *testing.T) {
	got := extractVideoIDFromURL("https://www.youtube.com/watch?v=QdSbtEo3x_Y")
	if got != "QdSbtEo3x_Y" {
		t.Errorf("extractVideoIDFromURL(watch?v) = %q, want %q", got, "QdSbtEo3x_Y")
	}
}

func TestExtractVideoIDFromURL_YoutuBe(t *testing.T) {
	got := extractVideoIDFromURL("https://youtu.be/RRJvrDKunyA")
	if got != "RRJvrDKunyA" {
		t.Errorf("extractVideoIDFromURL(youtu.be) = %q, want %q", got, "RRJvrDKunyA")
	}
}

func TestExtractVideoIDFromURL_NonYouTube_Empty(t *testing.T) {
	got := extractVideoIDFromURL("https://example.com/video.mp4")
	if got != "" {
		t.Errorf("extractVideoIDFromURL(non-YT) = %q, want empty", got)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test: validateCacheHit
// ─────────────────────────────────────────────────────────────────────

func TestValidateCacheHit_ValidFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "source.mp4")
	if err := os.WriteFile(path, []byte("fake-video-data-12345"), 0644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	entry := &SourceCacheEntry{
		CacheKey:  "test-key",
		LocalPath: path,
		FileSize:  fi.Size(),
	}
	if err := validateCacheHit(entry, testFS, zap.NewNop()); err != nil {
		t.Errorf("validateCacheHit returned err: %v (valid file should pass)", err)
	}
}

func TestValidateCacheHit_MissingFile(t *testing.T) {
	entry := &SourceCacheEntry{
		CacheKey:  "test-key",
		LocalPath: "/nonexistent/path/source.mp4",
		FileSize:  12345,
	}
	if err := validateCacheHit(entry, testFS, zap.NewNop()); err == nil {
		t.Error("validateCacheHit returned nil err on missing file (should fail)")
	}
}

func TestValidateCacheHit_SizeMismatch(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "source.mp4")
	if err := os.WriteFile(path, []byte("tiny"), 0644); err != nil {
		t.Fatal(err)
	}
	entry := &SourceCacheEntry{
		CacheKey:  "test-key",
		LocalPath: path,
		FileSize:  999999, // way bigger than actual
	}
	if err := validateCacheHit(entry, testFS, zap.NewNop()); err == nil {
		t.Error("validateCacheHit returned nil err on size mismatch (should fail)")
	}
}

func TestValidateCacheHit_NilEntry(t *testing.T) {
	if err := validateCacheHit(nil, testFS, zap.NewNop()); err == nil {
		t.Error("validateCacheHit returned nil err on nil entry (should fail)")
	}
}

func TestValidateCacheHit_EmptyPath(t *testing.T) {
	entry := &SourceCacheEntry{
		CacheKey:  "test-key",
		LocalPath: "",
		FileSize:  0,
	}
	if err := validateCacheHit(entry, testFS, zap.NewNop()); err == nil {
		t.Error("validateCacheHit returned nil err on empty path (should fail)")
	}
}

// TestValidateCacheHit_NilFS_FailClosed verifies that the
// PR-REFACTOR-P0-IO-BINDER guard fires when the composition root
// forgot to inject a LocalFSPort. The cache consumer (StockStager)
// propagates the failure to the download path so a runnable pipeline
// remains — but the wiring gap surfaces immediately.
func TestValidateCacheHit_NilFS_FailClosed(t *testing.T) {
	entry := &SourceCacheEntry{
		CacheKey:  "test-key",
		LocalPath: "/tmp/source.mp4",
		FileSize:  100,
	}
	err := validateCacheHit(entry, nil, zap.NewNop())
	if err == nil {
		t.Error("validateCacheHit returned nil err when LocalFSPort is nil (should fail closed)")
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test: copyFileToPath
// ─────────────────────────────────────────────────────────────────────

func TestCopyFileToPath_CopiesData(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.mp4")
	dst := filepath.Join(tmp, "dst.mp4")
	content := []byte("fake-video-content-for-copy-test")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyFileToPath(src, dst, testFS); err != nil {
		t.Fatalf("copyFileToPath returned err: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("copied content = %q, want %q", string(got), string(content))
	}
}

func TestCopyFileToPath_SrcMissing(t *testing.T) {
	tmp := t.TempDir()
	err := copyFileToPath(filepath.Join(tmp, "nonexistent"), filepath.Join(tmp, "dst"), testFS)
	if err == nil {
		t.Error("copyFileToPath returned nil err on missing source (should fail)")
	}
}

// TestCopyFileToPath_NilFS_FailClosed verifies the same composition
// wiring gap as TestValidateCacheHit_NilFS_FailClosed on the copy
// path. fail-closed surfaces the missing injection immediately.
func TestCopyFileToPath_NilFS_FailClosed(t *testing.T) {
	tmp := t.TempDir()
	err := copyFileToPath(filepath.Join(tmp, "src"), filepath.Join(tmp, "dst"), nil)
	if err == nil {
		t.Error("copyFileToPath returned nil err when LocalFSPort is nil (should fail closed)")
	}
}
