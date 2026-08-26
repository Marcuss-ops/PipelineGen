// Package texttracks — acquire_test.go: unit tests for the
// AcquireService (Fase 5, July 2026).
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026).
//
// Scope: table-driven tests for the 5-priority chain.
// Tests the AcquireService in isolation (no DB, no YouTube
// pipeline). The BackfillService integration is tested in
// backfill_test.go (if/when added).
package texttracks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubSubtitles is a minimal youtubeports.SubtitleFetcherPort stub.
// Returns a canned bundle (or error) for every FetchSegmentSubtitles
// call.
type stubSubtitles struct {
	bundle *detail.ResolvedTextBundle
	err    error
	calls  int
}

func (s *stubSubtitles) FetchSegmentSubtitles(_ context.Context, _ string, _, _ int) (*detail.ResolvedTextBundle, error) {
	s.calls++
	return s.bundle, s.err
}

// stubWhisper is a minimal youtubeports.WhisperTranscriberPort stub.
// Returns a canned TranscriptResult (or error) for every
// TranscribeAudioWithDetection call.
type stubWhisper struct {
	result detail.TranscriptResult
	err    error
	calls  int
}

func (s *stubWhisper) TranscribeAudioWithDetection(_ context.Context, _ string) (detail.TranscriptResult, error) {
	s.calls++
	return s.result, s.err
}

// writeVTTFile writes a minimal VTT file to a temp path and
// returns the path. Used by the priority-2 local-file test.
func writeVTTFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.vtt")
	err := os.WriteFile(path, []byte(content), 0o644)
	require.NoError(t, err)
	return path
}

// TestAcquireService_Priority2_LocalVTT verifies that when a
// local .vtt file exists next to the clip's local_path, the
// AcquireService returns the parsed text + cues WITHOUT calling
// the subtitles or whisper ports.
func TestAcquireService_Priority2_LocalVTT(t *testing.T) {
	dir := t.TempDir()
	clipPath := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(clipPath, []byte("fake"), 0o644))
	vttPath := filepath.Join(dir, "clip.vtt")
	vttContent := "WEBVTT\n\n00:00:00.000 --> 00:00:05.000\nHello world\n\n00:00:05.000 --> 00:00:10.000\nGoodbye world\n"
	require.NoError(t, os.WriteFile(vttPath, []byte(vttContent), 0o644))

	subs := &stubSubtitles{}
	whisp := &stubWhisper{}
	svc, err := NewAcquireService(subs, whisp, zap.NewNop())
	require.NoError(t, err)

	result, err := svc.Acquire(context.Background(), AcquireCommand{
		AssetID:   "yt_test_001",
		VideoID:   "vid-001",
		LocalPath: clipPath,
		Language:  "en",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 2, result.Priority, "priority should be 2 (local file)")
	assert.Equal(t, "Hello world\nGoodbye world", result.PlainText)
	assert.Len(t, result.Cues, 2, "two cues parsed from VTT")
	assert.Equal(t, "en", result.LanguageCode)
	assert.Equal(t, detail.TextTrackSource("local_file"), result.SourceType)
	assert.Equal(t, vttPath, result.SourcePath)

	// Priority 3+4 and 5 must NOT be called.
	assert.Equal(t, 0, subs.calls, "subtitles must not be called when local file is found")
	assert.Equal(t, 0, whisp.calls, "whisper must not be called when local file is found")
}

// TestAcquireService_Priority3Plus4_YouTubeSubs verifies that
// when no local file exists, the AcquireService falls through
// to the YouTube subtitles port and returns its bundle.
func TestAcquireService_Priority3Plus4_YouTubeSubs(t *testing.T) {
	dir := t.TempDir()
	clipPath := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(clipPath, []byte("fake"), 0o644))

	cues := []detail.TimedCue{
		{StartMs: 0, EndMs: 1000, Text: "sub line 1"},
		{StartMs: 1000, EndMs: 2000, Text: "sub line 2"},
	}
	subs := &stubSubtitles{
		bundle: &detail.ResolvedTextBundle{
			LanguageCode: "it",
			PlainText:    "sub line 1\nsub line 2",
			Cues:         cues,
			SourceType:   detail.TextSourceYouTubeSubtitle,
			IsOriginal:   true,
			Provider:     "youtube",
		},
	}
	whisp := &stubWhisper{}
	svc, err := NewAcquireService(subs, whisp, zap.NewNop())
	require.NoError(t, err)

	result, err := svc.Acquire(context.Background(), AcquireCommand{
		AssetID:   "yt_test_002",
		VideoID:   "vid-002",
		LocalPath: clipPath,
		Language:  "it",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 3, result.Priority, "priority should be 3 (YouTube subs)")
	assert.Equal(t, "it", result.LanguageCode)
	assert.Equal(t, detail.TextSourceYouTubeSubtitle, result.SourceType)
	assert.Equal(t, "sub line 1\nsub line 2", result.PlainText)
	assert.Len(t, result.Cues, 2)

	assert.Equal(t, 1, subs.calls, "subtitles called once")
	assert.Equal(t, 0, whisp.calls, "whisper must not be called when subtitles succeed")
}

// TestAcquireService_Priority5_Whisper verifies that when no
// local file AND no subtitles, the AcquireService falls through
// to Whisper and returns the typed result.
func TestAcquireService_Priority5_Whisper(t *testing.T) {
	dir := t.TempDir()
	clipPath := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(clipPath, []byte("fake"), 0o644))

	confidence := 0.92
	subs := &stubSubtitles{bundle: nil} // valid "not found"
	whisp := &stubWhisper{
		result: detail.TranscriptResult{
			Text:             "Whisper transcribed text",
			DetectedLanguage: "en",
			Confidence:       &confidence,
		},
	}
	svc, err := NewAcquireService(subs, whisp, zap.NewNop())
	require.NoError(t, err)

	result, err := svc.Acquire(context.Background(), AcquireCommand{
		AssetID:   "yt_test_003",
		VideoID:   "vid-003",
		LocalPath: clipPath,
		Language:  "en",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 5, result.Priority, "priority should be 5 (Whisper)")
	assert.Equal(t, "en", result.LanguageCode)
	assert.Equal(t, detail.TextSourceWhisper, result.SourceType)
	assert.Equal(t, "Whisper transcribed text", result.PlainText)
	require.NotNil(t, result.Confidence)
	assert.InDelta(t, 0.92, *result.Confidence, 0.001)

	assert.Equal(t, 1, subs.calls, "subtitles called once (returned nil)")
	assert.Equal(t, 1, whisp.calls, "whisper called once")
}

// TestAcquireService_AllFail verifies that when all 5 priorities
// fail, the AcquireService returns ErrNoSourceAcquired.
func TestAcquireService_AllFail(t *testing.T) {
	dir := t.TempDir()
	clipPath := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(clipPath, []byte("fake"), 0o644))

	subs := &stubSubtitles{bundle: nil}
	whisp := &stubWhisper{result: detail.TranscriptResult{Text: ""}} // empty result = not found
	svc, err := NewAcquireService(subs, whisp, zap.NewNop())
	require.NoError(t, err)

	result, err := svc.Acquire(context.Background(), AcquireCommand{
		AssetID:   "yt_test_004",
		VideoID:   "vid-004",
		LocalPath: clipPath,
		Language:  "en",
	})
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrNoSourceAcquired, "chain exhausted must return ErrNoSourceAcquired")

	assert.Equal(t, 1, subs.calls)
	assert.Equal(t, 1, whisp.calls)
}

// TestAcquireService_LocalVTT_SRTFormat verifies that .srt
// files are also parsed correctly.
func TestAcquireService_LocalVTT_SRTFormat(t *testing.T) {
	dir := t.TempDir()
	clipPath := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(clipPath, []byte("fake"), 0o644))
	srtPath := filepath.Join(dir, "clip.srt")
	srtContent := "1\n00:00:00,000 --> 00:00:05,000\nHello from SRT\n\n2\n00:00:05,000 --> 00:00:10,000\nGoodbye from SRT\n"
	require.NoError(t, os.WriteFile(srtPath, []byte(srtContent), 0o644))

	subs := &stubSubtitles{}
	whisp := &stubWhisper{}
	svc, err := NewAcquireService(subs, whisp, zap.NewNop())
	require.NoError(t, err)

	result, err := svc.Acquire(context.Background(), AcquireCommand{
		AssetID:   "yt_test_005",
		VideoID:   "vid-005",
		LocalPath: clipPath,
		Language:  "en",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 2, result.Priority)
	assert.Equal(t, "Hello from SRT\nGoodbye from SRT", result.PlainText)
	assert.Len(t, result.Cues, 2)
}

// TestParseSubtitleFile_VTT is a focused test for the VTT parser.
func TestParseSubtitleFile_VTT(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.vtt")
	content := "WEBVTT\n\n00:00:01.000 --> 00:00:04.500\nFirst cue\n\n00:00:05.000 --> 00:00:08.000\nSecond cue\nwith continuation\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	text, cues, err := ParseSubtitleFile(path)
	require.NoError(t, err)
	assert.Equal(t, "First cue\nSecond cue\nwith continuation", text)
	require.Len(t, cues, 2)
	assert.Equal(t, int64(1000), cues[0].StartMs)
	assert.Equal(t, int64(4500), cues[0].EndMs)
	assert.Equal(t, "First cue", cues[0].Text)
}

// TestParseSubtitleFile_SRT is a focused test for the SRT parser.
func TestParseSubtitleFile_SRT(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.srt")
	content := "1\n00:00:00,000 --> 00:00:03,000\nSRT line one\n\n2\n00:00:03,500 --> 00:00:07,000\nSRT line two\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	text, cues, err := ParseSubtitleFile(path)
	require.NoError(t, err)
	assert.Equal(t, "SRT line one\nSRT line two", text)
	require.Len(t, cues, 2)
	assert.Equal(t, int64(0), cues[0].StartMs)
	assert.Equal(t, int64(3000), cues[0].EndMs)
}

// TestParseSubtitleFile_Malformed verifies that a malformed
// file returns a typed error (the AcquireService logs + skips).
func TestParseSubtitleFile_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.vtt")
	// No WEBVTT header, no timestamp lines, just garbage.
	require.NoError(t, os.WriteFile(path, []byte("not a vtt file"), 0o644))

	_, _, err := ParseSubtitleFile(path)
	assert.Error(t, err, "malformed file must return a typed error")
}

// TestParseSubtitleFile_NotFound verifies that a non-existent
// file returns a typed error.
func TestParseSubtitleFile_NotFound(t *testing.T) {
	_, _, err := ParseSubtitleFile("/nonexistent/path/file.vtt")
	assert.Error(t, err)
}
