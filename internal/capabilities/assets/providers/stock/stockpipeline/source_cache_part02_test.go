package stockpipeline

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
	"strings"
	"sync"
	"testing"
)

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
	staged, err := stager.stageSource(context.Background(), ref)
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
	if _, err := stager.stageSource(context.Background(), ref); err != nil {
		t.Fatalf("first StageSource err: %v", err)
	}
	if fd.Count() != 1 {
		t.Fatalf("expected 1 download after first call, got %d", fd.Count())
	}
	if cache.Count() != 1 {
		t.Fatalf("expected 1 cache entry after first call, got %d", cache.Count())
	}

	staged2, err := stager.stageSource(context.Background(), ref)
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
	if _, err := stager.stageSource(context.Background(), ref); err != nil {
		t.Fatalf("first StageSource err: %v", err)
	}

	before := len(cap.Messages())
	if _, err := stager.stageSource(context.Background(), ref); err != nil {
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
	if _, err := stager.stageSource(context.Background(), refA); err != nil {
		t.Fatalf("clip A err: %v", err)
	}
	if _, err := stager.stageSource(context.Background(), refB); err != nil {
		t.Fatalf("clip B err: %v", err)
	}
	if fd.Count() != 2 {
		t.Errorf("expected 2 downloads for different sections, got %d", fd.Count())
	}
	if cache.Count() != 2 {
		t.Errorf("expected 2 cache entries (different keys per section), got %d", cache.Count())
	}
}
