// Package stockpipeline — source_cache_test.go (source cache unit tests).
//
// Tests the source cache key derivation, URL normalization, cache hit/miss
// logic, and invalidation paths. These are pure unit tests that do NOT
// require SQLite or a running server.
package stockpipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
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

// ───────────────────────────────────────────────────────────────────
// Integration tests (T1–T8): StockStager.StageSource end-to-end with
// fake downloader + in-memory cache + capture logger.
//
// These exercise the godlike/06 SSOT dedup contract from DoD §7
// ("una sola sorgente video scaricata, due intervalli = stesso
// hash, secondo run = SOURCE_CACHE_HIT") + DoD §8 ("2 richieste
// simultanee = 1 download"). The previous test fixture (hardcoded
// RRJvrDKunyA fallback in stock stager + ytdlpFetch closure) was
// retired; the cache + singleflight wiring is the sole resolver.
// ───────────────────────────────────────────────────────────────────
// Fake downloader — counts Download calls + writes fake mp4 bytes to
// OutputPath. Optional delay forces singleflight overlap (T7).
type fakeDownloader struct {
	mu            sync.Mutex
	downloadCount int
	writesBytes   []byte
	delay         time.Duration
	downloadedCh  chan struct{}
}

func newFakeDownloader(b []byte) *fakeDownloader {
	return &fakeDownloader{writesBytes: b, downloadedCh: make(chan struct{}, 100)}
}

func (f *fakeDownloader) Download(_ context.Context, req *SourceDownloadRequest) (*DownloadedSource, error) {
	f.mu.Lock()
	f.downloadCount++
	n := f.downloadCount
	f.mu.Unlock()
	if n == 1 {
		select {
		case f.downloadedCh <- struct{}{}:
		default:
		}
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	// Mimic the infra adapter: write OutputPath + ".mp4" (the resolved
	// path after yt-dlp's %(ext)s template resolution), then return
	// DownloadedSource with the resolved path and size.
	resolved := req.OutputPath + ".mp4"
	if err := os.WriteFile(resolved, f.writesBytes, 0644); err != nil {
		return nil, err
	}
	return &DownloadedSource{
		ResolvedPath: resolved,
		SizeBytes:    int64(len(f.writesBytes)),
	}, nil
}

func (f *fakeDownloader) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloadCount
}

// Fake in-memory SourceCache — satisfies both SourceCacheReader +
// SourceCacheWriter (composition root wires the same repo as both).
type fakeSourceCache struct {
	mu      sync.RWMutex
	entries map[string]*SourceCacheEntry
}

func newFakeSourceCache() *fakeSourceCache {
	return &fakeSourceCache{entries: make(map[string]*SourceCacheEntry)}
}

func (f *fakeSourceCache) GetByCacheKey(_ context.Context, cacheKey string) (*SourceCacheEntry, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if e, ok := f.entries[cacheKey]; ok {
		return e, nil
	}
	return nil, nil
}

func (f *fakeSourceCache) Upsert(_ context.Context, entry *SourceCacheEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[entry.CacheKey] = entry
	return nil
}

func (f *fakeSourceCache) Invalidate(_ context.Context, cacheKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, cacheKey)
	return nil
}

func (f *fakeSourceCache) Count() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.entries)
}

// loggerCapture — zapcore.Core that records every emitted entry's
// rendered message string. Used to assert on log output (e.g.
// "SOURCE_CACHE_HIT" in T3).
type loggerCapture struct {
	zapcore.Core
	mu      sync.Mutex
	entries []string
}

func newLoggerCapture() *loggerCapture {
	return &loggerCapture{Core: zapcore.NewNopCore()}
}

func (l *loggerCapture) With(fields []zapcore.Field) zapcore.Core {
	return l
}

// Enabled overrides the embedded NopCore's "false for everything"
// semantics so every emitted log entry reaches Check() → Write().
// Without this override zap's level filter would silently drop every
// entry (T2/T3 saw `got: []` until this fix).
func (l *loggerCapture) Enabled(_ zapcore.Level) bool {
	return true
}

func (l *loggerCapture) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if l.Enabled(ent.Level) {
		return ce.AddCore(ent, l)
	}
	return ce
}

func (l *loggerCapture) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	b.WriteString(ent.Message)
	for _, f := range fields {
		b.WriteByte(' ')
		b.WriteString(f.Key)
		b.WriteByte('=')
		b.WriteString(f.String)
	}
	l.entries = append(l.entries, b.String())
	return nil
}

func (l *loggerCapture) Messages() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.entries))
	copy(out, l.entries)
	return out
}

func (l *loggerCapture) HasMatch(needle string) bool {
	for _, m := range l.Messages() {
		if strings.Contains(m, needle) {
			return true
		}
	}
	return false
} // setupTestEnv wires a minimal Service + StockStager with all fakes
// and returns them for the integration tests. Only the fields
// StockStager.StageSource actually consults (cfg.Storage.TempPath,
// svc.log, svc.localFS) are populated; the rest stay nil.
//
// TempDir is set to the t.TempDir absolute path so cfg.Storage.TempPath()
// returns it verbatim (StorageConfig.FullPath has an "already-absolute"
// short-circuit — relative TempDir would join DataDir+TempDir and MkdirTemp
// would then fail because the joined subdir does not exist on disk).
func setupTestEnv(t *testing.T, downloader SourceDownloader) (*StockStager, *fakeSourceCache, *loggerCapture) {
	t.Helper()
	tmpRoot := t.TempDir()
	cap := newLoggerCapture()
	log := zap.New(cap)
	svc := &Service{
		runtime: &RuntimeConfig{WorkDir: tmpRoot, ClipDurationSec: 5, ChunkDurationSec: 25, MaxResults: 25, PolicyVersion: "test"},
		log:     log,
		localFS: testFS,
	}
	cache := newFakeSourceCache()
	stager := NewStockStager(svc).
		WithSourceCache(cache, cache).
		WithDownloader(downloader)
	return stager, cache, cap
}

// T1: cache miss → download + populate cache + return staged asset.
func TestStageSource_T1_CacheMissDownloadsAndPopulates(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t1"))
	stager, cache, _ := setupTestEnv(t, fd)

	ref := assets.SourceRef{URL: "https://www.youtube.com/watch?v=QdSbtEo3x_Y"}
	staged, err := stager.StageSource(context.Background(), ref)
	if err != nil {
		t.Fatalf("StageSource returned err: %v", err)
	}
	if staged == nil {
		t.Fatal("StageSource returned nil staged asset")
	}
	if fd.Count() != 1 {
		t.Errorf("expected 1 download, got %d", fd.Count())
	}
	if cache.Count() != 1 {
		t.Errorf("expected 1 cache entry, got %d", cache.Count())
	}
	fi, statErr := os.Stat(staged.LocalPath)
	if statErr != nil {
		t.Errorf("expected staged file on disk: %v", statErr)
	} else if fi.Size() != int64(len("fake-mp4-bytes-t1")) {
		t.Errorf("staged size = %d, want %d", fi.Size(), len("fake-mp4-bytes-t1"))
	}
}

// T2: second call on same URL → cache hit, no second download, log
// contains SOURCE_CACHE_HIT (DoD §7).
func TestStageSource_T2_CacheHitNoSecondDownload(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t2"))
	stager, cache, cap := setupTestEnv(t, fd)

	ref := assets.SourceRef{URL: "https://www.youtube.com/watch?v=QdSbtEo3x_Y"}
	if _, err := stager.StageSource(context.Background(), ref); err != nil {
		t.Fatalf("first StageSource err: %v", err)
	}
	if fd.Count() != 1 {
		t.Fatalf("expected 1 download after first call, got %d", fd.Count())
	}
	if cache.Count() != 1 {
		t.Fatalf("expected 1 cache entry after first call, got %d", cache.Count())
	}

	staged2, err := stager.StageSource(context.Background(), ref)
	if err != nil {
		t.Fatalf("second StageSource err: %v", err)
	}
	if fd.Count() != 1 {
		t.Errorf("expected STILL 1 download after cache hit, got %d (cache must prevent re-download)", fd.Count())
	}
	if staged2 == nil {
		t.Error("expected non-nil staged asset on cache hit")
	}
	// DoD §7 "file size validato": cache hit must round-trip the same
	// byte count as the original download. validateCacheHit enforces this
	// internally (else Invalidates); the test pins it explicitly.
	if staged2.Bytes != int64(len("fake-mp4-bytes-t2")) {
		t.Errorf("cache-hit bytes = %d, want %d (file size validation invariant)", staged2.Bytes, len("fake-mp4-bytes-t2"))
	}
	if !cap.HasMatch("SOURCE_CACHE_HIT") {
		t.Errorf("expected log to contain SOURCE_CACHE_HIT on cache hit, got: %v", cap.Messages())
	}
}

// T3: SOURCE_CACHE_HIT log entry is well-formed (message +
// cache_key + source_url + cached_path fields).
func TestStageSource_T3_CacheHitLogFormatting(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t3"))
	stager, _, cap := setupTestEnv(t, fd)

	ref := assets.SourceRef{
		URL:             "https://www.youtube.com/watch?v=QdSbtEo3x_Y",
		DownloadSection: "10-14",
	}
	if _, err := stager.StageSource(context.Background(), ref); err != nil {
		t.Fatalf("first StageSource err: %v", err)
	}

	before := len(cap.Messages())
	if _, err := stager.StageSource(context.Background(), ref); err != nil {
		t.Fatalf("second StageSource err: %v", err)
	}
	newLogs := cap.Messages()[before:]
	found := false
	for _, msg := range newLogs {
		if strings.Contains(msg, "SOURCE_CACHE_HIT") {
			found = true
			for _, field := range []string{"cache_key=", "source_url=", "cached_path="} {
				if !strings.Contains(msg, field) {
					t.Errorf("SOURCE_CACHE_HIT log missing %q field: %s", field, msg)
				}
			}
		}
	}
	if !found {
		t.Errorf("expected SOURCE_CACHE_HIT log entry on cache hit, got: %v", newLogs)
	}
}

// T4: different download sections on same URL → different cache keys,
// so 2 downloads, 2 cache entries (DoD §7 "Clip A 10–14s vs Clip B
// 30–34s" — different ranges, both honoured).
func TestStageSource_T4_DifferentSectionsTwoDownloads(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t4"))
	stager, cache, _ := setupTestEnv(t, fd)

	refA := assets.SourceRef{
		URL:             "https://www.youtube.com/watch?v=QdSbtEo3x_Y",
		DownloadSection: "10-14",
	}
	refB := assets.SourceRef{
		URL:             "https://www.youtube.com/watch?v=QdSbtEo3x_Y",
		DownloadSection: "30-34",
	}
	keyA := DeriveSourceCacheKey(refA.URL, refA.DownloadSection, "", false)
	keyB := DeriveSourceCacheKey(refB.URL, refB.DownloadSection, "", false)
	if keyA == keyB {
		t.Fatalf("different download sections produced same cache key (test pre-condition violated): %q", keyA)
	}
	if _, err := stager.StageSource(context.Background(), refA); err != nil {
		t.Fatalf("clip A err: %v", err)
	}
	if _, err := stager.StageSource(context.Background(), refB); err != nil {
		t.Fatalf("clip B err: %v", err)
	}
	if fd.Count() != 2 {
		t.Errorf("expected 2 downloads for different sections, got %d", fd.Count())
	}
	if cache.Count() != 2 {
		t.Errorf("expected 2 cache entries (different keys per section), got %d", cache.Count())
	}
}

// T5: cache hit but cached file is missing on disk → entry invalidated,
// fall through to re-download (DoD §7 "se corrotto, scaricato di nuovo").
func TestStageSource_T5_CacheFileMissing_InvalidatesAndRedownloads(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t5"))
	stager, cache, _ := setupTestEnv(t, fd)

	ref := assets.SourceRef{URL: "https://www.youtube.com/watch?v=QdSbtEo3x_Y"}
	if _, err := stager.StageSource(context.Background(), ref); err != nil {
		t.Fatalf("first StageSource err: %v", err)
	}
	cacheKey := DeriveSourceCacheKey(ref.URL, "", "", false)

	// Corrupt the entry: point LocalPath at a non-existent file.
	corruptedPath := "/nonexistent/missing-file.mp4"
	cache.mu.Lock()
	cache.entries[cacheKey].LocalPath = corruptedPath
	cache.mu.Unlock()

	beforeCount := fd.Count()
	if _, err := stager.StageSource(context.Background(), ref); err != nil {
		t.Fatalf("second StageSource err: %v", err)
	}
	if fd.Count() != beforeCount+1 {
		t.Errorf("expected +1 download (missing-file triggers re-download), before=%d after=%d", beforeCount, fd.Count())
	}
	// After invalidate + re-download, populateCache writes a NEW entry
	// under the same cache key. The right assertion is that the
	// corrupted LocalPath was replaced by the fresh download's
	// LocalPath (the previous "entry != nil" assertion was wrong
	// because the entry is re-populated by the same cache key).
	entry, getErr := cache.GetByCacheKey(context.Background(), cacheKey)
	if getErr != nil {
		t.Errorf("get after invalidate+repopulate err: %v", getErr)
	}
	if entry == nil {
		t.Fatal("expected cache entry re-populated after invalidate+re-download, got nil")
	}
	if entry.LocalPath == corruptedPath {
		t.Errorf("expected LocalPath to differ from corrupted=%q after refresh, got same value", corruptedPath)
	}
}

// T6: cache hit but cached file size mismatch → entry invalidated →
// fall through to re-download.
func TestStageSource_T6_CacheFileSizeMismatch_InvalidatesAndRedownloads(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t6"))
	stager, cache, _ := setupTestEnv(t, fd)

	ref := assets.SourceRef{URL: "https://www.youtube.com/watch?v=QdSbtEo3x_Y"}
	if _, err := stager.StageSource(context.Background(), ref); err != nil {
		t.Fatalf("first StageSource err: %v", err)
	}
	cacheKey := DeriveSourceCacheKey(ref.URL, "", "", false)

	cache.mu.Lock()
	cache.entries[cacheKey].FileSize = 999999999 // way bigger than actual
	cache.mu.Unlock()

	beforeCount := fd.Count()
	if _, err := stager.StageSource(context.Background(), ref); err != nil {
		t.Fatalf("second StageSource err: %v", err)
	}
	if fd.Count() != beforeCount+1 {
		t.Errorf("expected +1 download on size mismatch (before=%d after=%d)", beforeCount, fd.Count())
	}
}

// T7: 5 concurrent calls on same URL collapse to 1 yt-dlp download
// (DoD §8 "2 richieste simultanee collassino a 1 download"). The
// fake downloader sleeps 100ms per call so the 5 goroutines all
// overlap inside the singleflight callback window — without
// singleflight this test would show downloadCount=5.
func TestStageSource_T7_ConcurrentCollapsesToOneDownload(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t7"))
	fd.delay = 100 * time.Millisecond
	stager, _, _ := setupTestEnv(t, fd)

	ref := assets.SourceRef{URL: "https://www.youtube.com/watch?v=QdSbtEo3x_Y"}
	const N = 5
	var wg sync.WaitGroup
	wg.Add(N)
	barrier := make(chan struct{})

	results := make([]*assets.StagedAsset, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			<-barrier
			sa, err := stager.StageSource(context.Background(), ref)
			results[idx] = sa
			errs[idx] = err
		}(i)
	}
	close(barrier)
	wg.Wait()

	if fd.Count() != 1 {
		t.Errorf("expected 1 download after %d concurrent callers, got %d (singleflight must collapse)", N, fd.Count())
	}
	for i, sa := range results {
		if errs[i] != nil {
			t.Errorf("goroutine %d err: %v", i, errs[i])
			continue
		}
		if sa == nil {
			t.Errorf("goroutine %d returned nil staged asset", i)
		}
	}
}

// T9: concurrent callers that hit the same singleflight download must
// each receive an independent LocalPath. Cleanup of one job's temp
// directory must not remove the file used by another job (DoD §8 race
// condition guard).
func TestStageSource_T9_ConcurrentSingleflightReturnsIndependentPaths(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t9"))
	fd.delay = 100 * time.Millisecond
	stager, _, _ := setupTestEnv(t, fd)

	ref := assets.SourceRef{URL: "https://www.youtube.com/watch?v=QdSbtEo3x_Y"}
	const N = 5
	var wg sync.WaitGroup
	wg.Add(N)
	barrier := make(chan struct{})

	results := make([]*assets.StagedAsset, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			<-barrier
			sa, err := stager.StageSource(context.Background(), ref)
			results[idx] = sa
			errs[idx] = err
		}(i)
	}
	close(barrier)
	wg.Wait()

	if fd.Count() != 1 {
		t.Errorf("expected 1 download after %d concurrent callers, got %d (singleflight must collapse)", N, fd.Count())
	}

	paths := make(map[string]struct{})
	for i, sa := range results {
		if errs[i] != nil {
			t.Fatalf("goroutine %d err: %v", i, errs[i])
		}
		if sa == nil || sa.LocalPath == "" {
			t.Fatalf("goroutine %d returned nil/empty staged asset", i)
		}
		if _, exists := paths[sa.LocalPath]; exists {
			t.Errorf("duplicate LocalPath returned: %s", sa.LocalPath)
		}
		paths[sa.LocalPath] = struct{}{}
		if _, err := os.Stat(sa.LocalPath); err != nil {
			t.Errorf("goroutine %d staged file missing: %v", i, err)
		}
	}

	// Simulate cleanup of the first job and verify the others still have
	// their files on disk.
	cleanupDir := filepath.Dir(results[0].LocalPath)
	if err := os.RemoveAll(cleanupDir); err != nil {
		t.Fatalf("cleanup first job dir: %v", err)
	}
	for i, sa := range results {
		if i == 0 {
			continue
		}
		if _, err := os.Stat(sa.LocalPath); err != nil {
			t.Errorf("goroutine %d file was removed by another job's cleanup: %v", i, err)
		}
	}
}

// T10 (PR-STOCK-SOURCE-CACHE-LEASE): 5 concurrent StageSource callers
// race the singleflight callback, then each defers Cleanup and fires
// it IN ORDER (job[0] first). After job[0]'s Cleanup, jobs[1..4] must
// STILL each have a usable file on disk AND each can Cleanup
// themselves without race errors. This pins:
//
//   - 1 download (singleflight collision contract from T7/T9).
//   - 5 distinct LocalPaths (copy-to-own-tmp isolation).
//   - Cleanup of one job does NOT remove another job's file
//     (verdict's "Job A termina prima e chiama Cleanup, può cancellare
//     la sorgente mentre Job B o Job C stanno ancora usando il file"
//     scenario, now mitigated by the refcount lease AND confirmed
//     here at the test surface).
//   - Each Cleanup yields nil error even though all 5 share the
//     same singleflight leader's tmpDir (lease guards it).
//
// The lease's primary effect is observable: under the lease, the
// leader's tmpDir survives the first 4 Cleanups until the LAST
// Cleanup fires. Without the lease, copying logic alone would still
// protect the followers' own files (their copies are independent),
// but a synchronous early leader Cleanup would unlink the leader's
// tmpDir while followers are still in the singleflight callback
// wait window. The test pre-conditions block all followers from
// reading the leader's file mid-copy by introducing a 50ms delay in
// the fake downloader, simulating the realistic timing pressure.
func TestStageSource_T10_FiveConcurrentJobs_LeaseGuardsCleanupOrdering(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t10"))
	fd.delay = 50 * time.Millisecond
	stager, _, _ := setupTestEnv(t, fd)

	ref := assets.SourceRef{URL: "https://www.youtube.com/watch?v=QdSbtEo3x_Y"}
	const N = 5
	var wg sync.WaitGroup
	wg.Add(N)
	barrier := make(chan struct{})

	results := make([]*assets.StagedAsset, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			<-barrier
			sa, err := stager.StageSource(context.Background(), ref)
			results[idx] = sa
			errs[idx] = err
		}(i)
	}
	close(barrier)
	wg.Wait()

	// Single download expected — singleflight collapse intact.
	if fd.Count() != 1 {
		t.Errorf("expected 1 download after %d concurrent callers, got %d (singleflight must collapse)", N, fd.Count())
	}
	// No goroutine should have errored.
	for i, sa := range results {
		if errs[i] != nil {
			t.Fatalf("goroutine %d StageSource err: %v", i, errs[i])
		}
		if sa == nil || sa.LocalPath == "" {
			t.Fatalf("goroutine %d nil/empty staged asset", i)
		}
	}

	// Distinct LocalPaths — copy-to-own-tmp isolation.
	seenPaths := make(map[string]struct{}, N)
	for i, sa := range results {
		if _, dup := seenPaths[sa.LocalPath]; dup {
			t.Errorf("goroutine %d LocalPath %q duplicated (no per-caller isolation)", i, sa.LocalPath)
		}
		seenPaths[sa.LocalPath] = struct{}{}
		if _, err := os.Stat(sa.LocalPath); err != nil {
			t.Errorf("goroutine %d stat LocalPath: %v (file MUST be on disk pre-Cleanup)", i, err)
		}
	}

	// Cleanup goroutine[0] FIRST (worst-case ordering from the verdict).
	if err := stager.Cleanup(context.Background(), results[0]); err != nil {
		t.Fatalf("goroutine[0] Cleanup err: %v", err)
	}
	// NOTE: we deliberately don't assert that results[0]'s file is
	// missing after its own Cleanup — under PR-STOCK-SOURCE-CACHE-LEASE
	// (B1), if goroutine[0] is the singleflight LEADER, its own
	// ownDir removal is deferred to releaseSharedLease==last-ref.
	// Instead we verify the load-bearing invariant below: AFTER all
	// 5 Cleanups complete, every LocalPath is gone (each follower
	// removed its own tmpDir, the lease removed the leader's tmpDir
	// when the LAST ref was released).
	//
	// goroutines[1..4] must STILL have their files intact regardless
	// of whether goroutine[0] is leader or follower:
	//   - leader Cleanup (B1): ownDir deferred to lease (refCount>0
	//     for 4 outstanding refs) → followers' files intact.
	//   - follower Cleanup: ownDir = filepath.Dir(results[0].LocalPath)
	//     is THIS caller's tmp, independent of followers' tmps.
	for i := 1; i < N; i++ {
		if _, err := os.Stat(results[i].LocalPath); err != nil {
			t.Errorf("goroutine[%d] file was removed by goroutine[0]'s Cleanup: %v (lease/copy must isolate)", i, err)
		}
	}

	// Cleanup the rest — no race errors, no leakage.
	for i := 1; i < N; i++ {
		if err := stager.Cleanup(context.Background(), results[i]); err != nil {
			t.Errorf("goroutine[%d] Cleanup err: %v", i, err)
		}
	}
	// After all 5 Cleanups, every LocalPath must be gone (each goroutine
	// removed its own tmpDir AND the lease removed the shared leader
	// tmpDir once the LAST ref was released).
	for i := 0; i < N; i++ {
		if _, err := os.Stat(results[i].LocalPath); !os.IsNotExist(err) {
			t.Errorf("goroutine[%d] file still on disk after Cleanup: stat err = %v", i, err)
		}
	}
}

// T8: cache.Invalidate removes entry (lookup after Invalidate returns
// nil). Companion to T5/T6; pins the public-cache contract that the
// production repository (stocksourcecache.Repository.Invalidate)
// upholds symmetrically with the in-memory fake.
func TestStageSource_T8_CacheInvalidationRemovesEntry(t *testing.T) {
	fd := newFakeDownloader([]byte("fake-mp4-bytes-t8"))
	_, cache, _ := setupTestEnv(t, fd)

	// Seed the cache directly (no need to stage a full download).
	cacheKey := DeriveSourceCacheKey("https://www.youtube.com/watch?v=QdSbtEo3x_Y", "", "", false)
	if err := cache.Upsert(context.Background(), &SourceCacheEntry{
		CacheKey:  cacheKey,
		LocalPath: "/tmp/synthetic.mp4",
		FileSize:  42,
	}); err != nil {
		t.Fatalf("seed upsert err: %v", err)
	}
	if entry, err := cache.GetByCacheKey(context.Background(), cacheKey); err != nil || entry == nil {
		t.Fatalf("expected seeded entry present, got entry=%v err=%v", entry, err)
	}

	if err := cache.Invalidate(context.Background(), cacheKey); err != nil {
		t.Fatalf("invalidate err: %v", err)
	}
	entry, err := cache.GetByCacheKey(context.Background(), cacheKey)
	if err != nil {
		t.Errorf("get after invalidate err: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil entry after invalidate, got %+v", entry)
	}
	if cache.Count() != 0 {
		t.Errorf("expected empty cache after invalidate, got %d entries", cache.Count())
	}
}
