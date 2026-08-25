package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// fakePhashCache is an in-memory artifact cache that stores opaque bytes
// keyed by the canonical cache-key digest. Mirrors fakeWhisperCache.
type fakePhashCache struct {
	entries map[string][]byte
	lookups int
	stores  int
}

func (f *fakePhashCache) Lookup(_ context.Context, key capcache.Key, _ int64) (*capcache.Entry, bool, error) {
	f.lookups++
	digest, err := key.Digest()
	if err != nil {
		return nil, false, err
	}
	body, ok := f.entries[digest]
	if !ok {
		return nil, false, nil
	}
	return &capcache.Entry{CacheKey: digest, ArtifactSHA256: digest, SizeBytes: int64(len(body)), Status: "READY"}, true, nil
}

func (f *fakePhashCache) Store(_ context.Context, key capcache.Key, body io.Reader, _ string, _ int64) (*capcache.Entry, error) {
	digest, err := key.Digest()
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	f.entries[digest] = data
	f.stores++
	return &capcache.Entry{CacheKey: digest, ArtifactSHA256: digest, SizeBytes: int64(len(data)), Status: "READY"}, nil
}

func (f *fakePhashCache) Open(_ context.Context, entry *capcache.Entry) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(f.entries[entry.ArtifactSHA256])), nil
}

func (f *fakePhashCache) Invalidate(_ context.Context, key capcache.Key) error {
	if digest, err := key.Digest(); err == nil {
		delete(f.entries, digest)
	}
	return nil
}

func (f *fakePhashCache) Metrics(context.Context, string) (capcache.Metrics, error) {
	return capcache.Metrics{}, nil
}

var _ capcache.Cache = (*fakePhashCache)(nil)

// newPhashTestProcessor builds a processor wired to a mock embedding server
// and an in-memory artifact cache, with a fake registry that records pHash
// lookups but never reports a duplicate.
func newPhashTestProcessor(t *testing.T, srvURL string, cache *fakePhashCache, ff *fakeFFmpeg, registry *artifacts.SimpleRegistry) *Processor {
	t.Helper()
	p := NewProcessor(
		&fakeYTDLP{},
		&fakeHTTPDownloader{},
		ff,
		zap.NewNop(),
		ProcessorConfig{
			DataDir:            t.TempDir(),
			TempDir:            "tmp",
			VideoCfg:           mediaexec.NormalizeOptions{},
			EmbeddingServerURL: srvURL,
		},
		registry,
		&fakePublisher{},
	)
	p.SetArtifactCache(cache)
	return p
}

func TestPHashForVideo_ServesFromCacheOnWarmRun(t *testing.T) {
	var phashHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/phash" {
			http.NotFound(w, r)
			return
		}
		phashHits++
		_ = json.NewEncoder(w).Encode(map[string]string{"phash": "deadbeef-phash"})
	}))
	defer srv.Close()

	cache := &fakePhashCache{entries: map[string][]byte{}}
	ff := &fakeFFmpeg{}
	p := newPhashTestProcessor(t, srv.URL, cache, ff, &artifacts.SimpleRegistry{})

	videoPath := writeStagedFileForTest(t, "processed-video-bytes")

	phash1 := p.phashForVideo(context.Background(), videoPath)
	require.Equal(t, "deadbeef-phash", phash1)
	require.Equal(t, 1, ff.extractFrameCalls, "cold run must extract the frame once")

	phash2 := p.phashForVideo(context.Background(), videoPath)
	require.Equal(t, "deadbeef-phash", phash2)
	require.Equal(t, 1, ff.extractFrameCalls, "warm run must serve the pHash from cache without re-extracting the frame (ffmpeg dispatch avoided)")
	require.Equal(t, 1, phashHits, "warm run must skip the embedding HTTP call too")
	require.Equal(t, 1, cache.stores, "the pHash is stored exactly once (cold run)")
	require.GreaterOrEqual(t, cache.lookups, 2, "both runs consult the cache")
}

func TestProcess_WarmRunSkipsFrameExtraction(t *testing.T) {
	var phashHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/phash" {
			http.NotFound(w, r)
			return
		}
		phashHits++
		_ = json.NewEncoder(w).Encode(map[string]string{"phash": "deadbeef-phash"})
	}))
	defer srv.Close()

	cache := &fakePhashCache{entries: map[string][]byte{}}
	ff := &fakeFFmpeg{}
	registry := &artifacts.SimpleRegistry{
		PHashFn: func(_ context.Context, phash string) (string, error) {
			// Never a duplicate; just record the lookup stays fresh.
			return "", nil
		},
	}
	p := newPhashTestProcessor(t, srv.URL, cache, ff, registry)

	// Same staged bytes on both runs → same normalize key AND same
	// processed-video SHA → normalize + pHash both cache-hit on the warm run.
	run := func() {
		localPath := writeStagedFileForTest(t, "staged-bytes")
		result, err := p.Process(context.Background(), &asset.ProcessInput{
			ID:        "clip-phash-warm",
			Name:      "phash warm",
			LocalPath: localPath,
			OutputDir: t.TempDir(),
		})
		require.NoError(t, err)
		require.Equal(t, "processed", result.Status)
	}

	run()
	require.Equal(t, 1, ff.extractFrameCalls, "cold run extracts the pHash frame once")

	run()
	require.Equal(t, 1, ff.extractFrameCalls, "warm reprocess must NOT re-extract the pHash frame (ffmpeg dispatch = 0)")
	require.Equal(t, 1, phashHits, "warm reprocess must skip the embedding HTTP call")
}
