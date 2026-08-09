package videomuscles

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	pkgffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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

func TestYouTubeCutUsesConfiguredNVENCPolicy(t *testing.T) {
	runner := &youtubeCaptureRunner{}
	processor := pkgffmpeg.NewProcessorWithEncoder("ffmpeg", config.VideoEncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 23}).WithRunner(runner)

	require.NoError(t, processor.CutAndNormalize(
		context.Background(), "input.mp4", "output.mp4", "0", "4.000",
		canonicalYouTubeCutOptions(true),
	))

	require.True(t, youtubeHasArgPair(runner.args, "-c:v", "h264_nvenc"),
		"videomuscles must use the configured GPU encoder: %v", runner.args)
	require.False(t, youtubeHasArgPair(runner.args, "-c:v", "libx264"),
		"videomuscles must not fall back to libx264: %v", runner.args)
	require.True(t, youtubeHasArgPair(runner.args, "-preset", "p1"),
		"NVENC policy must normalize the default preset centrally: %v", runner.args)
	require.True(t, youtubeHasArgPair(runner.args, "-cq", "23"),
		"NVENC policy must retain the central quality default: %v", runner.args)
}

type youtubeCaptureRunner struct {
	args []string
}

func (r *youtubeCaptureRunner) Run(_ context.Context, _ string, args []string, _ process.Options) (*process.Result, error) {
	r.args = append([]string(nil), args...)
	return &process.Result{}, nil
}

func youtubeHasArgPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
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

// TestTempCutPathUniquenessAcrossSameOutputName pins the same uniqueness
// contract for the PreDownloadedPath (cut_*) temp file naming.
func TestTempCutPathUniquenessAcrossSameOutputName(t *testing.T) {
	p := newTestPipeline(t)
	outputName := "round-7-pacquiao-broner"

	path1 := p.tempCutPath(outputName)
	path2 := p.tempCutPath(outputName)

	require.NotEqual(t, path1, path2, "cut temp file paths for same output name must differ")
	require.Contains(t, filepath.Base(path1), outputName)
	require.Contains(t, filepath.Base(path2), outputName)
	require.Contains(t, filepath.Base(path1), "cut_")
	require.Contains(t, filepath.Base(path2), "cut_")
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
