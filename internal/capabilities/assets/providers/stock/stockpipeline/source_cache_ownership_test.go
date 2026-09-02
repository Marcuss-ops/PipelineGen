package stockpipeline

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	assets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
	"go.uber.org/zap"
)

type externalOwnedDownloader struct {
	mu    sync.Mutex
	count int
	path  string
	bytes []byte
}

func (d *externalOwnedDownloader) Download(_ context.Context, _ *SourceDownloadRequest) (*DownloadedSource, error) {
	d.mu.Lock()
	d.count++
	d.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(d.path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(d.path, d.bytes, 0o644); err != nil {
		return nil, err
	}
	return &DownloadedSource{ResolvedPath: d.path, SizeBytes: int64(len(d.bytes))}, nil
}

func (d *externalOwnedDownloader) Count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.count
}

func TestStageSource_ExternalOwnedCacheSurvivesCleanupAndHitsNextRun(t *testing.T) {
	workDir := t.TempDir()
	externalPath := filepath.Join(workDir, "acquisition_staging", "youtube-source.mp4")
	downloader := &externalOwnedDownloader{
		path:  externalPath,
		bytes: []byte("persistent-acquisition-owned-source"),
	}
	cache := newFakeSourceCache()
	svc := &Service{
		runtime: &RuntimeConfig{
			WorkDir:          workDir,
			ClipDurationSec:  5,
			ChunkDurationSec: 25,
			MaxResults:       25,
			PolicyVersion:    "test",
		},
		log:     zap.NewNop(),
		localFS: testFS,
	}
	stager := NewStockStager(svc).
		WithSourceCache(cache, cache).
		WithDownloader(downloader)
	ref := assets.SourceRef{URL: "https://www.youtube.com/watch?v=QdSbtEo3x_Y"}

	first, err := stager.stageSource(context.Background(), ref)
	if err != nil {
		t.Fatalf("first stageSource: %v", err)
	}
	if downloader.Count() != 1 {
		t.Fatalf("downloads after first stage = %d, want 1", downloader.Count())
	}
	if err := stager.cleanup(context.Background(), first); err != nil {
		t.Fatalf("first cleanup: %v", err)
	}
	if _, err := os.Stat(externalPath); err != nil {
		t.Fatalf("externally-owned cached source was removed by cleanup: %v", err)
	}

	second, err := stager.stageSource(context.Background(), ref)
	if err != nil {
		t.Fatalf("second stageSource: %v", err)
	}
	if downloader.Count() != 1 {
		t.Fatalf("second run invoked downloader again: got %d downloads, want 1", downloader.Count())
	}
	if second == nil || second.LocalPath == "" {
		t.Fatal("second stageSource returned nil/empty staged asset")
	}
	if err := stager.cleanup(context.Background(), second); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	if _, err := os.Stat(externalPath); err != nil {
		t.Fatalf("externally-owned cached source disappeared after cache-hit cleanup: %v", err)
	}
}