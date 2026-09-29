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
	// gotStart/gotEnd record the source-video window the caller asked for,
	// so the clip-window contract (StartSec/EndSec plumbing) is assertable.
	gotStart int
	gotEnd   int
}

func (s *stubSubtitles) FetchSegmentSubtitles(_ context.Context, _ string, startSec, endSec int) (*detail.ResolvedTextBundle, error) {
	s.calls++
	s.gotStart = startSec
	s.gotEnd = endSec
	return s.bundle, s.err
}

// TestAcquireService_Priority3Plus4_CuesRebasedOntoClipTimeline pins the
// timeline contract of the YouTube-subtitle leg: the source VTT's cues
// arrive on the SOURCE VIDEO timeline (146s for a clip cut at 146s) while
// priorities 2/2.5/5 all produce CLIP-local cues for the same asset. The
// result must be uniformly clip-local and clamped to the clip duration,
// otherwise the .ass validation and the clip render disagree with the
// stored segments (the "downloaded clip has no correct subs" bug).
func TestAcquireService_Priority3Plus4_CuesRebasedOntoClipTimeline(t *testing.T) {
	dir := t.TempDir()
	clipPath := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(clipPath, []byte("fake"), 0o644))

	subs := &stubSubtitles{
		bundle: &detail.ResolvedTextBundle{
			LanguageCode: "it",
			PlainText:    "subs della clip",
			Cues: []detail.TimedCue{
				{StartMs: 145_000, EndMs: 150_000, Text: "straddles the clip start"},
				{StartMs: 175_000, EndMs: 179_000, Text: "straddles the clip end"},
				{StartMs: 400_000, EndMs: 402_000, Text: "far outside the clip"},
			},
			SourceType: detail.TextSourceYouTubeSubtitle,
			IsOriginal: true,
			Provider:   "youtube",
		},
	}
	svc, err := NewAcquireService(subs, &stubWhisper{}, zap.NewNop())
	require.NoError(t, err)

	result, err := svc.Acquire(context.Background(), AcquireCommand{
		AssetID:   "yt_window_001",
		VideoID:   "vid-window-001",
		LocalPath: clipPath,
		Language:  "it",
		StartSec:  146,
		EndSec:    176, // 30s clip cut out of a longer source video
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// The window must reach the port verbatim (it is the port's slice
	// contract) and the returned cues must be clip-local.
	assert.Equal(t, 146, subs.gotStart)
	assert.Equal(t, 176, subs.gotEnd)

	require.Len(t, result.Cues, 2, "the far-outside cue must be dropped")
	assert.Equal(t, int64(0), result.Cues[0].StartMs, "first cue must start at the clip start (0ms)")
	assert.Equal(t, int64(4_000), result.Cues[0].EndMs, "start-straddling cue must clamp to 0")
	assert.LessOrEqual(t, result.Cues[1].EndMs, int64(30_000), "no cue may end after the 30s clip duration")
	for _, c := range result.Cues {
		assert.GreaterOrEqual(t, c.StartMs, int64(0))
		assert.Greater(t, c.EndMs, c.StartMs)
	}
}

// The whole-video window (0/0) is the no-window contract: cues keep their
// source timings (that is what GET /api/clips/transcript serves).
func TestAcquireService_Priority3Plus4_WholeVideoWindowKeepsSourceTimings(t *testing.T) {
	dir := t.TempDir()
	clipPath := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(clipPath, []byte("fake"), 0o644))

	subs := &stubSubtitles{bundle: &detail.ResolvedTextBundle{
		LanguageCode: "it",
		PlainText:    "intero video",
		Cues:         []detail.TimedCue{{StartMs: 146_000, EndMs: 148_000, Text: "sorgente"}},
		SourceType:   detail.TextSourceYouTubeSubtitle,
		IsOriginal:   true,
	}}
	svc, err := NewAcquireService(subs, &stubWhisper{}, zap.NewNop())
	require.NoError(t, err)

	result, err := svc.Acquire(context.Background(), AcquireCommand{
		AssetID:   "yt_window_002",
		VideoID:   "vid-window-002",
		LocalPath: clipPath,
		Language:  "it",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Cues, 1)
	assert.Equal(t, int64(146_000), result.Cues[0].StartMs, "0/0 must keep source-video timings untouched")
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
