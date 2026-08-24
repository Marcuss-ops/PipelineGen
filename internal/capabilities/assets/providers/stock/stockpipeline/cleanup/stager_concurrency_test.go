package cleanup

import (
	"context"
	"io"
	"io/fs"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

type gatedSourceDownloader struct {
	mu      sync.Mutex
	count   int
	started chan struct{}
	release chan struct{}
	once    sync.Once
	data    []byte
}

func (d *gatedSourceDownloader) Download(_ context.Context, req *SourceDownloadRequest) (*DownloadedSource, error) {
	d.mu.Lock()
	d.count++
	d.mu.Unlock()
	d.once.Do(func() { close(d.started) })
	<-d.release
	if err := os.WriteFile(req.OutputPath, d.data, 0644); err != nil {
		return nil, err
	}
	return &DownloadedSource{ResolvedPath: req.OutputPath, SizeBytes: int64(len(d.data))}, nil
}

func (d *gatedSourceDownloader) Count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.count
}

type blockedOpenFS struct {
	base        LocalFSPort
	openEntered chan struct{}
	openRelease chan struct{}
	once        sync.Once
}

func (f *blockedOpenFS) Stat(name string) (fs.FileInfo, error) { return f.base.Stat(name) }
func (f *blockedOpenFS) Open(name string) (io.ReadCloser, error) {
	file, err := f.base.Open(name)
	if err != nil {
		return nil, err
	}
	f.once.Do(func() { close(f.openEntered) })
	<-f.openRelease
	return file, nil
}
func (f *blockedOpenFS) Create(name string) (io.WriteCloser, error) { return f.base.Create(name) }
func (f *blockedOpenFS) MkdirTemp(dir, pattern string) (string, error) {
	return f.base.MkdirTemp(dir, pattern)
}
func (f *blockedOpenFS) Remove(name string) error    { return f.base.Remove(name) }
func (f *blockedOpenFS) RemoveAll(name string) error { return f.base.RemoveAll(name) }
func (f *blockedOpenFS) MkdirAll(path string, perm fs.FileMode) error {
	return f.base.MkdirAll(path, perm)
}
func (f *blockedOpenFS) CreateTemp(dir, pattern string) (string, io.WriteCloser, error) {
	return f.base.CreateTemp(dir, pattern)
}
func (f *blockedOpenFS) TempDir() string { return f.base.TempDir() }

type concurrentStageResult struct {
	index int
	asset *assets.StagedAsset
	err   error
}

func TestStageSource_ConcurrentCleanupDuringFollowerCopy(t *testing.T) {
	dl := &gatedSourceDownloader{
		started: make(chan struct{}),
		release: make(chan struct{}),
		data:    []byte("shared-source"),
	}
	stager, cache, _ := setupTestEnv(t, dl)
	fs := &blockedOpenFS{
		base:        testFS,
		openEntered: make(chan struct{}),
		openRelease: make(chan struct{}),
	}
	stager.svc.localFS = fs

	ref := assets.SourceRef{URL: "https://www.youtube.com/watch?v=QdSbtEo3x_Y"}
	cacheKey := DeriveSourceCacheKey(ref.URL, "", "", false)
	const jobs = 5
	results := make(chan concurrentStageResult, jobs)
	barrier := make(chan struct{})
	for i := 0; i < jobs; i++ {
		go func(index int) {
			<-barrier
			asset, err := stager.stageSource(context.Background(), ref)
			results <- concurrentStageResult{index: index, asset: asset, err: err}
		}(i)
	}
	close(barrier)

	select {
	case <-dl.started:
	case <-time.After(2 * time.Second):
		t.Fatal("singleflight download did not start")
	}
	if dl.Count() != 1 {
		t.Fatalf("expected one downloader call while jobs overlap, got %d", dl.Count())
	}
	close(dl.release)

	select {
	case <-fs.openEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("follower copy did not reach the blocked Open")
	}

	value, ok := stager.sharedRefs.Load(cacheKey)
	if !ok {
		t.Fatal("shared lease missing while follower copy is blocked")
	}
	lease := value.(*sharedSourceLease)
	lease.mu.Lock()
	ownerPath := lease.ownerPath
	lease.mu.Unlock()

	var leader concurrentStageResult
	followerResults := make(map[int]concurrentStageResult, jobs-1)
	deadline := time.After(2 * time.Second)
	for leader.asset == nil {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("job %d StageSource: %v", result.index, result.err)
			}
			if result.asset.LocalPath == ownerPath {
				leader = result
			} else {
				followerResults[result.index] = result
			}
		case <-deadline:
			t.Fatal("leader did not return while follower copy was blocked")
		}
	}

	if err := stager.cleanup(context.Background(), leader.asset); err != nil {
		t.Fatalf("leader cleanup: %v", err)
	}
	entry, err := cache.GetByCacheKey(context.Background(), cacheKey)
	if err != nil || entry == nil {
		t.Fatalf("cache entry after leader cleanup: entry=%v err=%v", entry, err)
	}
	if _, err := os.Stat(entry.LocalPath); err != nil {
		t.Fatalf("leader cleanup removed shared source while follower copy was blocked: %v", err)
	}

	close(fs.openRelease)
	for len(followerResults) < jobs-1 {
		result := <-results
		if result.err != nil {
			t.Fatalf("job %d StageSource after release: %v", result.index, result.err)
		}
		followerResults[result.index] = result
	}
	for index, result := range followerResults {
		if result.asset == nil {
			t.Fatalf("job %d returned nil asset", index)
		}
		if err := stager.cleanup(context.Background(), result.asset); err != nil {
			t.Fatalf("job %d cleanup: %v", index, err)
		}
	}
	if _, err := os.Stat(entry.LocalPath); !os.IsNotExist(err) {
		t.Fatalf("shared source still exists after final cleanup: %v", err)
	}
}
