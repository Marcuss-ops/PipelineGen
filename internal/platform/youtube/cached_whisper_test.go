package youtube

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

type fakeWhisperCache struct {
	entries map[string][]byte
	lookups int
	stores  int
}

func (f *fakeWhisperCache) Lookup(_ context.Context, key capcache.Key, _ int64) (*capcache.Entry, bool, error) {
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
func (f *fakeWhisperCache) Store(_ context.Context, key capcache.Key, body io.Reader, mime string, _ int64) (*capcache.Entry, error) {
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
func (f *fakeWhisperCache) Open(_ context.Context, entry *capcache.Entry) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(f.entries[entry.ArtifactSHA256])), nil
}
func (f *fakeWhisperCache) Invalidate(_ context.Context, key capcache.Key) error {
	digest, _ := key.Digest()
	delete(f.entries, digest)
	return nil
}
func (f *fakeWhisperCache) Metrics(context.Context, string) (capcache.Metrics, error) {
	return capcache.Metrics{}, nil
}

var _ capcache.Cache = (*fakeWhisperCache)(nil)

type countingWhisper struct{ calls int }

func (w *countingWhisper) TranscribeAudio(ctx context.Context, path string) (string, error) {
	result, err := w.TranscribeAudioWithDetection(ctx, path)
	return result.Text, err
}
func (w *countingWhisper) TranscribeAudioWithDetection(context.Context, string) (detail.TranscriptResult, error) {
	w.calls++
	return detail.TranscriptResult{Text: "cached transcript", DetectedLanguage: "en"}, nil
}

func TestCachedWhisperUsesSourceBytesNotLocalPath(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.mp4")
	second := filepath.Join(dir, "second.mp4")
	if err := os.WriteFile(first, []byte("same source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("same source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	inner := &countingWhisper{}
	cache := &fakeWhisperCache{entries: map[string][]byte{}}
	decorated, err := NewCachedWhisperTranscriber(inner, cache, "whisper/bridge-v1", zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decorated.TranscribeAudioWithDetection(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	result, err := decorated.TranscribeAudioWithDetection(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "cached transcript" {
		t.Fatalf("cached result=%+v", result)
	}
	if inner.calls != 1 {
		t.Fatalf("same source bytes should invoke Whisper once, calls=%d", inner.calls)
	}
	if cache.stores != 1 || cache.lookups != 2 {
		t.Fatalf("cache activity stores=%d lookups=%d, want 1/2", cache.stores, cache.lookups)
	}
}
