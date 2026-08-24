package stockpipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// newStockPerformanceBenchStager wires the production StockStager with the
// existing in-memory cache and downloader fakes. It intentionally uses the
// typed LocalFSPort and a benchmark-owned temporary directory: no network,
// SQLite, Drive, or yt-dlp process is involved.
func newStockPerformanceBenchStager(b *testing.B) (*StockStager, *fakeSourceCache, *fakeDownloader, string) {
	b.Helper()
	workDir := b.TempDir()
	cache := newFakeSourceCache()
	downloader := newFakeDownloader([]byte("benchmark-video-bytes"))
	svc := &Service{
		runtime: &RuntimeConfig{WorkDir: workDir, ClipDurationSec: 5, ChunkDurationSec: 25, MaxResults: 25, PolicyVersion: "benchmark"},
		log:     zap.NewNop(),
		localFS: filesystem.NewLocal(),
	}
	stager := NewStockStager(svc).
		WithSourceCache(cache, cache).
		WithDownloader(downloader)
	return stager, cache, downloader, workDir
}

func BenchmarkStockStageSource_CacheHit(b *testing.B) {
	stager, cache, _, workDir := newStockPerformanceBenchStager(b)
	ref := assets.SourceRef{URL: "https://example.test/benchmark-hit.mp4"}
	key := DeriveSourceCacheKey(ref.URL, "", "", false)
	sourcePath := filepath.Join(workDir, "source.mp4")
	if err := filesystem.NewLocal().MkdirAll(workDir, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := writeBenchmarkSource(sourcePath); err != nil {
		b.Fatal(err)
	}
	cacheSize := int64(len("benchmark-video-bytes"))
	if err := cache.Upsert(context.Background(), &SourceCacheEntry{
		CacheKey:  key,
		SourceURL: ref.URL,
		LocalPath: sourcePath,
		FileSize:  cacheSize,
	}); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		staged, err := stager.stageSource(context.Background(), ref)
		if err != nil {
			b.Fatal(err)
		}
		if err := stager.cleanup(context.Background(), staged); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStockStageSource_CacheMissDownload(b *testing.B) {
	stager, _, downloader, _ := newStockPerformanceBenchStager(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ref := assets.SourceRef{URL: fmt.Sprintf("https://example.test/benchmark-miss-%d.mp4", i)}
		staged, err := stager.stageSource(context.Background(), ref)
		if err != nil {
			b.Fatal(err)
		}
		if err := stager.cleanup(context.Background(), staged); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if got := downloader.Count(); got != b.N {
		b.Fatalf("downloads=%d, want %d", got, b.N)
	}
}

func BenchmarkStockStageSource_ConcurrentCacheHit(b *testing.B) {
	stager, cache, _, _ := newStockPerformanceBenchStager(b)
	ref := assets.SourceRef{URL: "https://example.test/benchmark-concurrent-hit.mp4"}
	key := DeriveSourceCacheKey(ref.URL, "", "", false)
	sourcePath := filepath.Join(b.TempDir(), "concurrent-source.mp4")
	if err := writeBenchmarkSource(sourcePath); err != nil {
		b.Fatal(err)
	}
	if err := cache.Upsert(context.Background(), &SourceCacheEntry{
		CacheKey:  key,
		SourceURL: ref.URL,
		LocalPath: sourcePath,
		FileSize:  int64(len("benchmark-video-bytes")),
	}); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			staged, err := stager.stageSource(context.Background(), ref)
			if err != nil {
				b.Fatal(err)
			}
			if err := stager.cleanup(context.Background(), staged); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func writeBenchmarkSource(path string) error {
	fs := filesystem.NewLocal()
	file, err := fs.Create(path)
	if err != nil {
		return err
	}
	if _, err := file.Write([]byte("benchmark-video-bytes")); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
