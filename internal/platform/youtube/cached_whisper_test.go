package youtube

import (
	"bytes"
	"context"
	"encoding/json"
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

type countingWhisper struct {
	calls int
	// empty makes the fake return a contentless result (a silent/music-only
	// clip) instead of the default usable transcript.
	empty bool
}

func (w *countingWhisper) TranscribeAudio(ctx context.Context, path string) (string, error) {
	result, err := w.TranscribeAudioWithDetection(ctx, path)
	return result.Text, err
}
func (w *countingWhisper) TranscribeAudioWithDetection(context.Context, string) (detail.TranscriptResult, error) {
	w.calls++
	if w.empty {
		return detail.TranscriptResult{DetectedLanguage: "und"}, nil
	}
	return detail.TranscriptResult{Text: "cached transcript", DetectedLanguage: "en"}, nil
}

// TestCachedWhisperDoesNotCacheEmptyTranscript pins the poisoned-cache fix: an
// ASR run that legitimately found no speech must stay a success, but the empty
// answer must not become a READY artifact that answers every later attempt.
func TestCachedWhisperDoesNotCacheEmptyTranscript(t *testing.T) {
	source := filepath.Join(t.TempDir(), "silent.mp4")
	if err := os.WriteFile(source, []byte("silent source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	inner := &countingWhisper{empty: true}
	cache := &fakeWhisperCache{entries: map[string][]byte{}}
	decorated, err := NewCachedWhisperTranscriber(inner, cache, "whisper/bridge-v1", zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := decorated.TranscribeAudioWithDetection(context.Background(), source); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if inner.calls != 2 {
		t.Fatalf("an empty transcript must not be served from cache: calls=%d, want 2", inner.calls)
	}
	if cache.stores != 0 {
		t.Fatalf("an empty transcript must not be stored: stores=%d, want 0", cache.stores)
	}
}

// TestCachedWhisperHealsPoisonedEmptyEntry covers the seven entries already
// written by the old behavior: reading one is a cache miss (invalidated),
// the ASR runs again, and the real result replaces it.
func TestCachedWhisperHealsPoisonedEmptyEntry(t *testing.T) {
	source := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(source, []byte("poisoned source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, ok := whisperCacheKey(source, "whisper/bridge-v1")
	if !ok {
		t.Fatal("test source is not cacheable")
	}
	digest, err := key.Digest()
	if err != nil {
		t.Fatal(err)
	}
	poisoned, err := json.Marshal(detail.TranscriptResult{DetectedLanguage: "und"})
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeWhisperCache{entries: map[string][]byte{digest: poisoned}}
	inner := &countingWhisper{}
	decorated, err := NewCachedWhisperTranscriber(inner, cache, "whisper/bridge-v1", zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	result, err := decorated.TranscribeAudioWithDetection(context.Background(), source)
	if err != nil {
		t.Fatalf("TranscribeAudioWithDetection: %v", err)
	}
	if result.Text != "cached transcript" {
		t.Fatalf("poisoned entry was served: result=%+v", result)
	}
	if inner.calls != 1 || cache.stores != 1 {
		t.Fatalf("healing activity calls=%d stores=%d, want 1/1", inner.calls, cache.stores)
	}
	if _, stillThere := cache.entries[digest]; !stillThere {
		t.Fatal("the healed result must replace the poisoned entry")
	}
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
