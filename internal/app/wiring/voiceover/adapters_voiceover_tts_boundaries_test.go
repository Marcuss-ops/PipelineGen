// Package app — adapters_voiceover_tts_boundaries_test.go
//
// Tests for the Edge TTS word-boundary normalization seam: the
// useCaseTTSAdapter surfaces the bridge's metadata.jsonl (captured in the
// SAME synthesis stream as the audio) as provider-neutral RawSpeechBoundary
// values. The parser must preserve integer microsecond offsets verbatim,
// skip non-WordBoundary lines defensively, and tolerate corrupt lines
// (truncated/partial writes carrying NUL bytes) by skipping them and
// reporting the count so the adapter can retry the synthesis for a clean
// capture instead of shipping a silently incomplete timing artifact.
package voiceover

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/platform/audio"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeBoundaryFile writes the given metadata.jsonl lines to a temp file
// and returns its path.
func writeBoundaryFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stub.mp3.metadata.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// TestParseEdgeWordBoundaries_PreservesMicroseconds pins the canonical
// normalization contract: the bridge already converts Edge 100ns ticks to
// integer microseconds exactly once; the Go side must preserve those values
// verbatim and never re-derive or round them.
func TestParseEdgeWordBoundaries_PreservesMicroseconds(t *testing.T) {
	path := writeBoundaryFile(t, ""+
		"{\"type\": \"WordBoundary\", \"text\": \"Jackie\", \"start_us\": 320000, \"end_us\": 710000}\n"+
		"{\"type\": \"WordBoundary\", \"text\": \"Chan\", \"start_us\": 720000, \"end_us\": 1050000}\n")

	got, skipped, err := parseEdgeWordBoundaries(path)
	require.NoError(t, err)
	require.Zero(t, skipped)
	require.Len(t, got, 2)
	assert.Equal(t, "Jackie", got[0].Text)
	assert.Equal(t, int64(320000), got[0].StartUS)
	assert.Equal(t, int64(710000), got[0].EndUS)
	assert.Equal(t, "Chan", got[1].Text)
	assert.Equal(t, int64(720000), got[1].StartUS)
	assert.Equal(t, int64(1050000), got[1].EndUS)
}

// TestParseEdgeWordBoundaries_SkipsNonWordBoundary pins the defensive skip:
// a non-WordBoundary line (e.g. a future SentenceBoundary) must not corrupt
// the word-level array.
func TestParseEdgeWordBoundaries_SkipsNonWordBoundary(t *testing.T) {
	path := writeBoundaryFile(t, ""+
		"{\"type\": \"SentenceBoundary\", \"text\": \"Hi.\", \"start_us\": 0, \"end_us\": 100000}\n"+
		"{\"type\": \"WordBoundary\", \"text\": \"Hi\", \"start_us\": 0, \"end_us\": 100000}\n")

	got, skipped, err := parseEdgeWordBoundaries(path)
	require.NoError(t, err)
	require.Zero(t, skipped)
	require.Len(t, got, 1)
	assert.Equal(t, "Hi", got[0].Text)
}

// TestParseEdgeWordBoundaries_EmptyFile pins the empty-metadata behavior: a
// zero-byte (or whitespace-only) file yields zero boundaries without error,
// so the adapter reports no boundary mode rather than a fake empty artifact.
func TestParseEdgeWordBoundaries_EmptyFile(t *testing.T) {
	path := writeBoundaryFile(t, "\n\n")
	got, skipped, err := parseEdgeWordBoundaries(path)
	require.NoError(t, err)
	require.Zero(t, skipped)
	assert.Empty(t, got)
}

// TestParseEdgeWordBoundaries_SkipsCorruptLine pins the tolerant contract:
// a non-JSON line is skipped (and counted) rather than aborting the parse,
// so one truncated line never fails the whole synthesis while the valid
// boundaries are still returned.
func TestParseEdgeWordBoundaries_SkipsCorruptLine(t *testing.T) {
	path := writeBoundaryFile(t, ""+
		"{\"type\": \"WordBoundary\", \"text\": \"ok\", \"start_us\": 0, \"end_us\": 100000}\n"+
		"this is not json\n"+
		"{\"type\": \"WordBoundary\", \"text\": \"fine\", \"start_us\": 100000, \"end_us\": 200000}\n")

	got, skipped, err := parseEdgeWordBoundaries(path)
	require.NoError(t, err)
	require.Equal(t, 1, skipped)
	require.Len(t, got, 2)
	assert.Equal(t, "ok", got[0].Text)
	assert.Equal(t, "fine", got[1].Text)
}

// TestParseEdgeWordBoundaries_SkipsNulByteCorruptLine pins the specific
// corruption observed in production: a truncated/partial bridge write leaves
// raw NUL bytes in the metadata.jsonl, which json.Unmarshal rejects. Those
// lines must be skipped (and counted), never fatal.
func TestParseEdgeWordBoundaries_SkipsNulByteCorruptLine(t *testing.T) {
	path := writeBoundaryFile(t, ""+
		"{\"type\": \"WordBoundary\", \"text\": \"ok\", \"start_us\": 0, \"end_us\": 100000}\n"+
		"\x00garbage\n"+
		"{\"type\": \"WordBoundary\", \"text\": \"fine\", \"start_us\": 100000, \"end_us\": 200000}\n")

	got, skipped, err := parseEdgeWordBoundaries(path)
	require.NoError(t, err)
	require.Equal(t, 1, skipped)
	require.Len(t, got, 2)
	assert.Equal(t, "ok", got[0].Text)
	assert.Equal(t, "fine", got[1].Text)
}

// TestParseEdgeWordBoundaries_MissingFileFailsClosed pins the I/O contract:
// a missing metadata file surfaces the open error (never an empty success).
func TestParseEdgeWordBoundaries_MissingFileFailsClosed(t *testing.T) {
	_, _, err := parseEdgeWordBoundaries(filepath.Join(t.TempDir(), "does-not-exist.metadata.jsonl"))
	require.Error(t, err)
}

// fakeTTSGenerator is a test double for the ttsGenerator port. Each call
// returns a result whose MetadataPath points at metadataPaths[callIndex]
// (or an empty metadata path once the slice is exhausted).
type fakeTTSGenerator struct {
	calls         int
	metadataPaths []string
	durations     []time.Duration
}

func (f *fakeTTSGenerator) Generate(_ context.Context, _ *audioasset.AudioInput) (*audioasset.AudioResult, error) {
	idx := f.calls
	f.calls++
	res := &audioasset.AudioResult{
		LocalPath:     "/tmp/stub.mp3",
		LegacyFileMD5: "stub-hash",
		Duration:      3 * time.Second,
		Voice:         "it-IT-ElsaNeural",
	}
	if idx < len(f.durations) {
		res.Duration = f.durations[idx]
	}
	if idx < len(f.metadataPaths) && f.metadataPaths[idx] != "" {
		res.MetadataPath = f.metadataPaths[idx]
	}
	return res, nil
}

func TestUseCaseTTSAdapterRetriesWhenBoundariesOutlastAudio(t *testing.T) {
	dir := t.TempDir()
	shortPath := filepath.Join(dir, "short.metadata.jsonl")
	completePath := filepath.Join(dir, "complete.metadata.jsonl")
	require.NoError(t, os.WriteFile(shortPath, []byte(
		"{\"type\": \"WordBoundary\", \"text\": \"Milano\", \"start_us\": 50000, \"end_us\": 550000}\n"), 0o644))
	require.NoError(t, os.WriteFile(completePath, []byte(
		"{\"type\": \"WordBoundary\", \"text\": \"Milano\", \"start_us\": 50000, \"end_us\": 300000}\n"), 0o644))

	fake := &fakeTTSGenerator{
		metadataPaths: []string{shortPath, completePath},
		durations:     []time.Duration{360 * time.Millisecond, time.Second},
	}
	out, err := newUseCaseTTSAdapter(fake).Synthesize(context.Background(), voiceover.TTSInput{
		Text: "Milano e Brescia emergono", Language: "it", Filename: "out.mp3", OutputDir: dir,
	})
	require.NoError(t, err)
	require.Equal(t, 2, fake.calls, "a truncated audio capture must trigger one full synthesis retry")
	require.Equal(t, time.Second, out.Duration)
	require.Len(t, out.WordBoundaries, 1)
	assert.Equal(t, int64(300000), out.WordBoundaries[0].EndUS)
}

func TestUseCaseTTSAdapterFailsWhenRetryStillTruncatesAudio(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.metadata.jsonl")
	secondPath := filepath.Join(dir, "second.metadata.jsonl")
	boundary := "{\"type\": \"WordBoundary\", \"text\": \"word\", \"start_us\": 0, \"end_us\": 550000}\n"
	require.NoError(t, os.WriteFile(firstPath, []byte(boundary), 0o644))
	require.NoError(t, os.WriteFile(secondPath, []byte(boundary), 0o644))
	fake := &fakeTTSGenerator{
		metadataPaths: []string{firstPath, secondPath},
		durations:     []time.Duration{360 * time.Millisecond, 360 * time.Millisecond},
	}
	_, err := newUseCaseTTSAdapter(fake).Synthesize(context.Background(), voiceover.TTSInput{
		Text: "a long sentence", Language: "it", Filename: "out.mp3", OutputDir: dir,
	})
	require.Error(t, err)
	require.Equal(t, 2, fake.calls)
}

// TestUseCaseTTSAdapter_RetriesOnCorruptBoundaries pins the retry contract:
// when the first capture carries a corrupt boundary line, the adapter
// re-runs the synthesis once and, on a clean capture, uses the retry's
// boundaries (audio + boundaries stay from the SAME synthesis pass).
func TestUseCaseTTSAdapter_RetriesOnCorruptBoundaries(t *testing.T) {
	dir := t.TempDir()
	corruptPath := filepath.Join(dir, "corrupt.mp3.metadata.jsonl")
	cleanPath := filepath.Join(dir, "clean.mp3.metadata.jsonl")
	require.NoError(t, os.WriteFile(corruptPath, []byte(
		"{\"type\": \"WordBoundary\", \"text\": \"ok\", \"start_us\": 0, \"end_us\": 100000}\n"+
			"\x00garbage\n"+
			"{\"type\": \"WordBoundary\", \"text\": \"fine\", \"start_us\": 100000, \"end_us\": 200000}\n"), 0o644))
	require.NoError(t, os.WriteFile(cleanPath, []byte(
		"{\"type\": \"WordBoundary\", \"text\": \"clean\", \"start_us\": 0, \"end_us\": 100000}\n"), 0o644))

	fake := &fakeTTSGenerator{metadataPaths: []string{corruptPath, cleanPath}}
	adapter := newUseCaseTTSAdapter(fake)
	out, err := adapter.Synthesize(context.Background(), voiceover.TTSInput{
		Text: "x", Language: "it", Filename: "out.mp3", OutputDir: dir,
	})
	require.NoError(t, err)
	require.Equal(t, 2, fake.calls, "a corrupt capture must trigger exactly one retry")
	require.Len(t, out.WordBoundaries, 1)
	assert.Equal(t, "clean", out.WordBoundaries[0].Text)
}

// TestUseCaseTTSAdapter_KeepsDegradedCaptureWhenRetryAlsoCorrupt pins the
// fallback: when the retry is ALSO corrupt, the adapter keeps the valid
// boundaries from the first capture instead of failing or returning nothing.
func TestUseCaseTTSAdapter_KeepsDegradedCaptureWhenRetryAlsoCorrupt(t *testing.T) {
	dir := t.TempDir()
	corruptPath := filepath.Join(dir, "corrupt.mp3.metadata.jsonl")
	corruptPath2 := filepath.Join(dir, "corrupt2.mp3.metadata.jsonl")
	require.NoError(t, os.WriteFile(corruptPath, []byte(
		"{\"type\": \"WordBoundary\", \"text\": \"ok\", \"start_us\": 0, \"end_us\": 100000}\n"+
			"\x00garbage\n"), 0o644))
	require.NoError(t, os.WriteFile(corruptPath2, []byte("\x00more-garbage\n"), 0o644))

	fake := &fakeTTSGenerator{metadataPaths: []string{corruptPath, corruptPath2}}
	adapter := newUseCaseTTSAdapter(fake)
	out, err := adapter.Synthesize(context.Background(), voiceover.TTSInput{
		Text: "x", Language: "it", Filename: "out.mp3", OutputDir: dir,
	})
	require.NoError(t, err)
	require.Equal(t, 2, fake.calls)
	require.Len(t, out.WordBoundaries, 1)
	assert.Equal(t, "ok", out.WordBoundaries[0].Text)
}
