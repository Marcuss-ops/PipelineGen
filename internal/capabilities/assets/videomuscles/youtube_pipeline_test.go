package videomuscles

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCanonicalYouTubeCutOptionsDelegatesEncoderPolicy(t *testing.T) {
	opts := canonicalYouTubeCutOptions(true)

	require.False(t, opts.NoAudio)
	require.Empty(t, opts.Codec,
		"videomuscles must delegate codec selection to the configured FFmpeg policy")
	require.Empty(t, opts.Preset)
	require.Zero(t, opts.CRF)

	noAudio := canonicalYouTubeCutOptions(false)
	require.True(t, noAudio.NoAudio)
}

func TestBuildYouTubeSectionDownloadRequestDisablesForceKeyframes(t *testing.T) {
	req := buildYouTubeSectionDownloadRequest(YouTubeCutRequest{
		URL:            "https://www.youtube.com/watch?v=example",
		ForceKeyframes: true,
	}, "/tmp/raw.mp4", "*00:00:01.000-00:00:05.000", true)

	require.False(t, req.ForceKeyframes,
		"the canonical YouTube path must leave section cutting to CutAndNormalize")
	require.Equal(t, []string{"*00:00:01.000-00:00:05.000"}, req.DownloadSections)
	require.Equal(t, "mp4", req.MergeFormat)
	require.True(t, req.UseCookies)
}

func TestUsableCachedClipIgnoresEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(path, []byte{}, 0644))

	ok, err := fileutil.UsableCachedClip(path)
	require.NoError(t, err)
	require.False(t, ok)

	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr))
}

func TestUsableCachedClipAcceptsNonEmptyRegularFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(path, []byte("video-bytes"), 0644))

	ok, err := fileutil.UsableCachedClip(path)
	require.NoError(t, err)
	require.True(t, ok)
}

// newTestPipeline creates a minimal Pipeline for testing temp path helpers.
// No ffmpeg/yt-dlp wired — only Storage.TempPath() is consumed.
func newTestPipeline(t *testing.T) *Pipeline {
	t.Helper()
	cfg := &config.Config{}
	cfg.Storage.DataDir = t.TempDir()
	// TempPath() returns DataDir/tmp if not set explicitly
	lg := zap.NewNop()
	return &Pipeline{cfg: cfg, log: lg}
}

// TestTempRawPathUniquenessAcrossSameVideoID pins the no_audio bug fix:
// two calls for the same video ID must produce distinct temp file paths
// so concurrent requests don't overwrite each other's downloads.
//
// Bug scenario (E2E battery Test 02 + Test 11 on same video):
//
//	Test 02: normal download (0-5s, keep_audio=true)  -> raw_jNQXAC9IVRw.mp4
//	Test 11: no_audio download (10-15s, keep_audio=false) -> raw_jNQXAC9IVRw.mp4
//
// Both used the SAME deterministic temp path, causing the second download
// to overwrite the first's temp file before ffmpeg processed it.
//
// Fix: temp file names now include fileutil.RandomString(8) suffix.
func TestTempRawPathUniquenessAcrossSameVideoID(t *testing.T) {
	p := newTestPipeline(t)
	videoID := "jNQXAC9IVRw"

	path1 := p.tempRawPath(videoID)
	path2 := p.tempRawPath(videoID)

	require.NotEqual(t, path1, path2, "temp file paths for same video ID must differ (no_audio collision guard)")
	require.Contains(t, filepath.Base(path1), videoID)
	require.Contains(t, filepath.Base(path2), videoID)
	require.Contains(t, filepath.Base(path1), "raw_")
	require.Contains(t, filepath.Base(path2), "raw_")
}

// TestTempRawPathContainsVideoIDForDebuggability ensures the video ID
// is preserved in the temp path for operator debuggability.
func TestTempRawPathContainsVideoIDForDebuggability(t *testing.T) {
	p := newTestPipeline(t)
	path := p.tempRawPath("vdC5GXxS-qU")
	base := filepath.Base(path)

	require.Contains(t, base, "vdC5GXxS-qU", "video ID must be visible in temp path for debugging")
	require.Contains(t, base, ".mp4", "temp path must end with .mp4")
}

// TestRandomStringSuffixLength pins the suffix length at 8 hex chars
// (the exact value passed to fileutil.RandomString in the production code).
func TestRandomStringSuffixLength(t *testing.T) {
	suffix := fileutil.RandomString(8)
	require.Len(t, suffix, 8, "RandomString(8) must produce exactly 8 chars")
	require.Regexp(t, `^[0-9a-f]+$`, suffix, "RandomString must be hex")
}

// recordingClipProcessor captures the media operation the pipeline asks for and
// the exact seek window it computed. No ffmpeg is spawned.
type recordingClipProcessor struct {
	called     string
	start, end string
	sourcePath string
	noAudio    bool
}

func (p *recordingClipProcessor) CutCopy(_ context.Context, sourcePath, _, start, end string, noAudio bool) error {
	p.called = "copy"
	p.sourcePath = sourcePath
	p.start, p.end, p.noAudio = start, end, noAudio
	return nil
}

func (p *recordingClipProcessor) CutAndNormalize(
	_ context.Context, sourcePath, _, start, end string, opts mediaexec.CutAndNormalizeOptions,
) error {
	p.called = "normalize"
	p.sourcePath = sourcePath
	p.start, p.end = start, end
	p.noAudio = opts.NoAudio
	return nil
}

// TestDownloadAndCutYouTubeVideo_AppliesSourceOffsetToLocalSeek pins the seek
// arithmetic of the merged-window path: the segment timestamps are ABSOLUTE in
// the source, while the staged file holds a SECTION of it, so the local cut must
// seek at (start - SourceOffsetSec). Getting this wrong publishes the wrong
// seconds with no visible failure.
func TestDownloadAndCutYouTubeVideo_AppliesSourceOffsetToLocalSeek(t *testing.T) {
	cases := []struct {
		name       string
		cutMode    mediaexec.CutMode
		start      float64
		duration   float64
		offset     float64
		wantCalled string
		wantStart  string
		wantEnd    string
	}{
		{
			name:       "normalize seeks inside the staged section",
			cutMode:    mediaexec.CutModeNormalize,
			start:      610,
			duration:   20,
			offset:     600,
			wantCalled: "normalize",
			wantStart:  "00:00:10.000",
			wantEnd:    "00:00:30.000",
		},
		{
			name:       "copy seeks inside the staged section",
			cutMode:    mediaexec.CutModeCopy,
			start:      610,
			duration:   20,
			offset:     600,
			wantCalled: "copy",
			wantStart:  "00:00:10.000",
			wantEnd:    "00:00:30.000",
		},
		{
			name:       "whole-source stage keeps absolute timestamps",
			cutMode:    mediaexec.CutModeNormalize,
			start:      90,
			duration:   15,
			offset:     0,
			wantCalled: "normalize",
			wantStart:  "00:01:30.000",
			wantEnd:    "00:01:45.000",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proc := &recordingClipProcessor{}
			p := NewPipeline(&config.Config{}, zap.NewNop(), proc)
			outDir := t.TempDir()
			staged := filepath.Join(outDir, "section.mp4")

			result, err := p.DownloadAndCutYouTubeVideo(context.Background(), YouTubeCutRequest{
				URL:               "https://www.youtube.com/watch?v=vid",
				VideoID:           "vid",
				Start:             tc.start,
				Duration:          tc.duration,
				OutputName:        "clip",
				OutputDir:         outDir,
				PreDownloadedPath: staged,
				SourceOffsetSec:   tc.offset,
				CutMode:           tc.cutMode,
				SkipMetadataFetch: true,
			})
			require.NoError(t, err)
			require.Equal(t, tc.wantCalled, proc.called)
			require.Equal(t, staged, proc.sourcePath, "the cut must read the staged section")
			require.Equal(t, tc.wantStart, proc.start)
			require.Equal(t, tc.wantEnd, proc.end)
			require.NotNil(t, result)
		})
	}
}

// TestDownloadAndCutYouTubeVideo_FailsClosedOnInconsistentOffset pins the two
// caller-bug guards: an offset without a staged file, and a segment that starts
// before its own section. Both would silently publish the wrong seconds if
// clamped or ignored instead of refused.
func TestDownloadAndCutYouTubeVideo_FailsClosedOnInconsistentOffset(t *testing.T) {
	p := NewPipeline(&config.Config{}, zap.NewNop(), &recordingClipProcessor{})
	outDir := t.TempDir()

	_, err := p.DownloadAndCutYouTubeVideo(context.Background(), YouTubeCutRequest{
		URL:             "https://www.youtube.com/watch?v=vid",
		VideoID:         "vid",
		Start:           30,
		Duration:        10,
		OutputName:      "clip",
		OutputDir:       outDir,
		SourceOffsetSec: 100,
		CutMode:         mediaexec.CutModeNormalize,
	})
	require.Error(t, err, "an offset with no pre-downloaded path must fail closed")

	_, err = p.DownloadAndCutYouTubeVideo(context.Background(), YouTubeCutRequest{
		URL:               "https://www.youtube.com/watch?v=vid",
		VideoID:           "vid",
		Start:             30,
		Duration:          10,
		OutputName:        "clip",
		OutputDir:         outDir,
		PreDownloadedPath: filepath.Join(outDir, "section.mp4"),
		SourceOffsetSec:   100,
		CutMode:           mediaexec.CutModeNormalize,
	})
	require.Error(t, err, "a segment starting before its staged section must fail closed")
}
