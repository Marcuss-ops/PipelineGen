package adapters

// cliprender_streaming_transcriber_test.go — unit test for the streaming
// PCM transcriber (spec §4: zero temp WAV). Uses stub ffmpeg/bridge scripts
// so the test is deterministic and needs no real ffmpeg or Whisper model.
//
// The stub ffmpeg writes raw s16le PCM to stdout (asserting it was NOT asked
// to write a WAV) and the stub bridge consumes stdin → JSON, proving the
// MP4 → PCM pipe → Whisper chain with no audio intermediate on disk.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

// writeStubScript writes an executable python3 stub and returns its path.
func writeStubScript(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n"+body), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
	return path
}

const pcmDecoderStub = `
import sys
# Fail loudly if any output file (e.g. .wav) was requested instead of pipe.
args = sys.argv[1:]
if any(a.endswith(".wav") or a.endswith(".mp3") for a in args):
    print("stub ffmpeg: refuse to write an audio file: " + " ".join(args), file=sys.stderr)
    sys.exit(9)
if "pipe:1" not in args:
    print("stub ffmpeg: expected PCM on stdout (pipe:1), got: " + " ".join(args), file=sys.stderr)
    sys.exit(9)
sys.stdout.buffer.write(b"\x00\x00" * 16000)  # 32000 bytes ≈ 1s of 16k mono s16
sys.stdout.buffer.flush()
`

// bridgeStubTemplate: %s is the JSON payload the stub bridge prints after
// consuming the PCM stdin to EOF.
const bridgeStubTemplate = `
import json, sys
data = sys.stdin.buffer.read()
if len(data) == 0:
    print(json.dumps({"error": "empty stdin PCM"}), file=sys.stderr)
    sys.exit(2)
print(%s)
`

func newTestStreamingTranscriber(t *testing.T, ffmpegPath, bridgePath string) *ClipRenderStreamingTranscriber {
	t.Helper()
	return &ClipRenderStreamingTranscriber{
		pythonBin:  "python3",
		scriptPath: bridgePath,
		ffmpegPath: ffmpegPath,
		timeout:    0,
		log:        zap.NewNop(),
	}
}

func TestStreamingTranscriber_PCMOnly_NoWAV(t *testing.T) {
	ffmpeg := writeStubScript(t, "stub_ffmpeg.py", pcmDecoderStub)
	bridge := writeStubScript(t, "stub_bridge.py", fmt.Sprintf(bridgeStubTemplate, `json.dumps({"text": "hello streaming world", "detected_language": "en", "confidence": 0.91, "duration_ms": 1000, "cues": [{"start_ms": 0, "end_ms": 1000, "text": "hello streaming world"}]})`))
	tr := newTestStreamingTranscriber(t, ffmpeg, bridge)

	res, err := tr.TranscribeStream(context.Background(), &cliprender.MaterializedAsset{
		AssetID:   "asset-1",
		LocalPath: "/fake/source.mp4",
	}, "en")
	if err != nil {
		t.Fatalf("TranscribeStream: %v", err)
	}
	if res.Text != "hello streaming world" {
		t.Fatalf("expected bridge text, got %q", res.Text)
	}
	if res.Language != "en" {
		t.Fatalf("expected language en, got %q", res.Language)
	}
	if res.Confidence == nil || *res.Confidence != 0.91 {
		t.Fatalf("expected confidence 0.91, got %v", res.Confidence)
	}
	if len(res.Cues) != 1 || res.Cues[0].StartMs != 0 || res.Cues[0].EndMs != 1000 {
		t.Fatalf("expected 1 cue 0..1000ms, got %+v", res.Cues)
	}
	// The stub ffmpeg exits 9 if a WAV was requested — the test already
	// failed above in that case. Double-check zero-duration.
	if res.DurationMS != 1000 {
		t.Fatalf("expected duration 1000ms, got %d", res.DurationMS)
	}
}

func TestStreamingTranscriber_BridgeFailure_IsTyped(t *testing.T) {
	ffmpeg := writeStubScript(t, "stub_ffmpeg.py", pcmDecoderStub)
	bridge := writeStubScript(t, "stub_bridge.py", `
sys.stderr.write("model not loaded\n")
sys.exit(1)
`)
	tr := newTestStreamingTranscriber(t, ffmpeg, bridge)

	_, err := tr.TranscribeStream(context.Background(), &cliprender.MaterializedAsset{
		AssetID:   "asset-1",
		LocalPath: "/fake/source.mp4",
	}, "en")
	if err == nil {
		t.Fatalf("expected error from bridge failure, got nil")
	}
	if !strings.Contains(err.Error(), "whisper bridge subprocess") {
		t.Fatalf("expected typed bridge error, got: %v", err)
	}
}

func TestStreamingTranscriber_EmptyTranscript_FailClosed(t *testing.T) {
	ffmpeg := writeStubScript(t, "stub_ffmpeg.py", pcmDecoderStub)
	bridge := writeStubScript(t, "stub_bridge.py", fmt.Sprintf(bridgeStubTemplate, `json.dumps({"text": "", "detected_language": "und", "confidence": 0.0, "cues": []})`))
	tr := newTestStreamingTranscriber(t, ffmpeg, bridge)

	_, err := tr.TranscribeStream(context.Background(), &cliprender.MaterializedAsset{
		AssetID:   "asset-1",
		LocalPath: "/fake/source.mp4",
	}, "en")
	if err == nil {
		t.Fatalf("expected fail-closed error on empty transcript, got nil")
	}
	if !errors.Is(err, cliprender.ErrTranscriptGenerationUnavailable) {
		t.Fatalf("expected ErrTranscriptGenerationUnavailable, got: %v", err)
	}
}

// fakeStreamingCache is the narrow artifact-cache fake: enough to prove the
// streaming transcriber reuses a result by source bytes + language.
type fakeStreamingCache struct {
	entries map[string][]byte
	lookups int
	stores  int
}

func (f *fakeStreamingCache) Lookup(_ context.Context, key capcache.Key, _ int64) (*capcache.Entry, bool, error) {
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

func (f *fakeStreamingCache) Store(_ context.Context, key capcache.Key, body io.Reader, _ string, _ int64) (*capcache.Entry, error) {
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

func (f *fakeStreamingCache) Open(_ context.Context, entry *capcache.Entry) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(f.entries[entry.ArtifactSHA256])), nil
}

func (f *fakeStreamingCache) Invalidate(_ context.Context, key capcache.Key) error {
	digest, _ := key.Digest()
	delete(f.entries, digest)
	return nil
}

func (f *fakeStreamingCache) Metrics(context.Context, string) (capcache.Metrics, error) {
	return capcache.Metrics{}, nil
}

var _ capcache.Cache = (*fakeStreamingCache)(nil)

func TestStreamingTranscriber_ReusesCachedTranscriptBySourceAndLanguage(t *testing.T) {
	ffmpeg := writeStubScript(t, "stub_ffmpeg.py", pcmDecoderStub)
	runsPath := filepath.Join(t.TempDir(), "bridge-runs.txt")
	payload := `json.dumps({"text": "hello streaming world", "language": "en", "detected_language": "en", "confidence": 0.91, "duration_ms": 1000, "cues": [{"start_ms": 0, "end_ms": 1000, "text": "hello streaming world"}]})`
	bridge := writeStubScript(t, "stub_bridge.py", fmt.Sprintf(`
import json, sys
data = sys.stdin.buffer.read()
if len(data) == 0:
    sys.exit(2)
with open(%q, "a") as f:
    f.write("run\n")
print(%s)
`, runsPath, payload))

	tr := newTestStreamingTranscriber(t, ffmpeg, bridge)
	cache := &fakeStreamingCache{entries: map[string][]byte{}}
	tr.SetArtifactCache(cache, "whisper/stream/test")
	source := &cliprender.MaterializedAsset{
		AssetID:   "asset-1",
		LocalPath: "/fake/source.mp4",
		SHA256:    strings.Repeat("a", 64),
	}

	first, err := tr.TranscribeStream(context.Background(), source, "en")
	if err != nil {
		t.Fatalf("first TranscribeStream: %v", err)
	}
	// A NEW transcriber instance with the SAME cache models the next run: no
	// in-memory state may be needed for the reuse to happen.
	secondTranscriber := newTestStreamingTranscriber(t, ffmpeg, bridge)
	secondTranscriber.SetArtifactCache(cache, "whisper/stream/test")
	second, err := secondTranscriber.TranscribeStream(context.Background(), source, "en")
	if err != nil {
		t.Fatalf("second TranscribeStream: %v", err)
	}
	if !second.Reused {
		t.Fatal("a cache hit must report Reused=true")
	}
	if first.Text != second.Text || len(second.Cues) != 1 {
		t.Fatalf("cached result differs: first=%+v second=%+v", first, second)
	}
	if got := countBridgeRuns(t, runsPath); got != 1 {
		t.Fatalf("bridge invoked %d times for identical bytes+language, want 1", got)
	}
	if cache.lookups != 2 || cache.stores != 1 {
		t.Fatalf("cache activity lookups=%d stores=%d, want 2/1", cache.lookups, cache.stores)
	}

	// A different language is a different cache address: a forced decode must
	// never be reused as another language's transcript.
	if _, err := tr.TranscribeStream(context.Background(), source, "it"); err != nil {
		t.Fatalf("third TranscribeStream (it): %v", err)
	}
	if got := countBridgeRuns(t, runsPath); got != 2 {
		t.Fatalf("bridge invoked %d times after a language change, want 2", got)
	}
	if cache.stores != 2 {
		t.Fatalf("stores=%d after a language change, want 2", cache.stores)
	}
}

func countBridgeRuns(t *testing.T, path string) int {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read bridge counter: %v", err)
	}
	return strings.Count(string(body), "run\n")
}

func TestNewClipRenderStreamingTranscriber_FailClosed(t *testing.T) {
	// From the package test cwd the repo-root bridge script is not
	// resolvable and/or python3 is absent — either way construction must
	// fail closed with a typed error (never a half-wired transcriber).
	_, err := NewClipRenderStreamingTranscriber(nil, zap.NewNop())
	if err == nil {
		t.Fatalf("expected construction error, got nil")
	}
	if !strings.Contains(err.Error(), "streaming transcriber:") {
		t.Fatalf("expected typed construction error, got: %v", err)
	}
}
