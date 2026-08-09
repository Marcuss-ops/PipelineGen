package ffmpeg

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireFFmpeg skips the test unless both ffmpeg and ffprobe are on PATH.
func requireFFmpeg(t *testing.T) string {
	t.Helper()
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("ffprobe not on PATH: %v", err)
	}
	return ffmpegPath
}

// generateNonCanonicalSource creates a synthetic MP4 with 1280×720/30 fps
// video and 48 kHz stereo AAC audio. The clip is intentionally NOT the
// canonical profile so we can verify that CutReencode/CutReencodeBatch
// force it to 1920×1080/24 fps.
func generateNonCanonicalSource(t *testing.T, dir string, durationSec int) string {
	t.Helper()
	src := filepath.Join(dir, fmt.Sprintf("source_1280x720_30fps_%ds.mp4", durationSec))
	cmd := exec.Command("ffmpeg",
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=duration=%d:size=1280x720:rate=30", durationSec),
		"-f", "lavfi", "-i", "sine=frequency=1000:duration="+strconv.Itoa(durationSec),
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264",
		"-c:a", "aac",
		"-ar", "48000",
		"-ac", "2",
		src,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to generate synthetic source: %v\n%s", err, string(out))
	}
	return src
}

// assertCanonicalClip verifies that the file at path matches the canonical
// clip profile produced by the stock pipeline.
func assertCanonicalClip(t *testing.T, p *Processor, path string, expectedDuration float64) {
	t.Helper()
	info, err := p.Probe(context.Background(), path)
	require.NoError(t, err, "failed to probe %s", path)

	assert.Equal(t, 1920, info.Width, "width must be canonical for %s", path)
	assert.Equal(t, 1080, info.Height, "height must be canonical for %s", path)
	assert.InDelta(t, 24.0, info.FPS, 0.5, "fps must be ~24 for %s", path)
	assert.Equal(t, "h264", info.VideoCodec, "video codec must be h264 for %s", path)
	assert.Equal(t, "yuv420p", info.PixelFormat, "pixel format must be yuv420p for %s", path)
	assert.Equal(t, "aac", info.AudioCodec, "audio codec must be aac for %s", path)
	assert.True(t, info.HasAudio, "clip must retain audio for %s", path)
	assert.InDelta(t, expectedDuration, info.Duration.Seconds(), 0.25,
		"duration must match the cut for %s", path)
}

// TestCutReencode_NormalizesInputToCanonicalProfile verifies that a
// 1280×720/30 fps source is re-encoded to the canonical 1920×1080/24 fps
// profile by CutReencode.
func TestCutReencode_NormalizesInputToCanonicalProfile(t *testing.T) {
	ffmpegPath := requireFFmpeg(t)
	dir := t.TempDir()
	src := generateNonCanonicalSource(t, dir, 5)

	p := NewProcessorWithEncoder(ffmpegPath, config.VideoEncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23})
	out := filepath.Join(dir, "cut.mp4")
	err := p.CutReencode(context.Background(), src, out, "1.000", "4.000", false, "", "", 0)
	require.NoError(t, err)

	assertCanonicalClip(t, p, out, 3.0)
}

// TestCutReencodeBatch_NormalizesInputToCanonicalProfile verifies that a
// 1280×720/30 fps source is re-encoded to the canonical 1920×1080/24 fps
// profile for every job produced by CutReencodeBatch.
func TestCutReencodeBatch_NormalizesInputToCanonicalProfile(t *testing.T) {
	ffmpegPath := requireFFmpeg(t)
	dir := t.TempDir()
	src := generateNonCanonicalSource(t, dir, 6)

	p := NewProcessorWithEncoder(ffmpegPath, config.VideoEncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23})
	out1 := filepath.Join(dir, "clip1.mp4")
	out2 := filepath.Join(dir, "clip2.mp4")
	jobs := []CutJob{
		{StartSec: 0.5, EndSec: 2.5, Output: out1},
		{StartSec: 3.0, EndSec: 5.0, Output: out2},
	}
	err := p.CutReencodeBatch(context.Background(), src, jobs, false, "", "", 0)
	require.NoError(t, err)

	assertCanonicalClip(t, p, out1, 2.0)
	assertCanonicalClip(t, p, out2, 2.0)
}
