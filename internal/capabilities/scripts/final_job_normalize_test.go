package scriptgeneration

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// These tests exercise the real ffmpeg/ffprobe binaries; they are skipped when
// the tools are absent (the production path fails closed without them, but a
// unit-test host is allowed not to have them).

func requireFFmpegTools(t *testing.T) (string, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	return ffmpeg, ffprobe
}

func runFF(t *testing.T, bin string, args ...string) {
	t.Helper()
	if out, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v: %s", bin, strings.Join(args, " "), err, out)
	}
}

func firstVideoPTS(t *testing.T, ffprobe, path string) float64 {
	t.Helper()
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time", "-of", "csv=p=0", "-read_intervals", "%+#1", path).Output()
	if err != nil {
		t.Fatalf("probe %s: %v", path, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if value, convErr := strconv.ParseFloat(strings.TrimSpace(line), 64); convErr == nil {
			return value
		}
	}
	t.Fatalf("no video packet PTS in %s: %q", path, out)
	return 0
}

func videoCodec(t *testing.T, ffprobe, path string) string {
	t.Helper()
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		t.Fatalf("probe codec %s: %v", path, err)
	}
	return strings.TrimSpace(string(out))
}

// makeOffsetSource builds a 1s MP4 whose first video packet starts at a
// positive PTS (0.2s), reproducing the Drive-clip condition the normalization
// exists for. codec is the video encoder used for the fixture.
func makeOffsetSource(t *testing.T, ffmpeg, dir, codec string) string {
	t.Helper()
	base := filepath.Join(dir, "base.mp4")
	runFF(t, ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=duration=1:size=160x120:rate=24",
		"-c:v", codec, "-pix_fmt", "yuv420p", base)
	offset := filepath.Join(dir, "offset.mp4")
	runFF(t, ffmpeg, "-v", "error", "-y", "-fflags", "+genpts", "-itsoffset", "0.2", "-i", base, "-c", "copy", offset)
	return offset
}

func assertZeroPTS(t *testing.T, ffprobe, path string) {
	t.Helper()
	if got := firstVideoPTS(t, ffprobe, path); got < -0.001 || got > 0.001 {
		t.Fatalf("normalized first PTS = %v, want 0", got)
	}
}

// TestNormalizeFinalJobVideoBackground_RemuxFastPathZeroesPositivePTS pins the
// fast path: an H.264/yuv420p source is remuxed with a full stream copy (no
// frame decoded or re-encoded) and the first PTS still lands on zero.
func TestNormalizeFinalJobVideoBackground_RemuxFastPathZeroesPositivePTS(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpegTools(t)
	dir := t.TempDir()
	source := makeOffsetSource(t, ffmpeg, dir, "libx264")
	if pts := firstVideoPTS(t, ffprobe, source); pts <= 0 {
		t.Fatalf("fixture must start at a positive PTS, got %v", pts)
	}

	plan := capabilityoverlay.OverlayPlan{Source: &capabilityoverlay.OverlaySource{AssetID: "drive-background-05", LocalPath: source}}
	normalized, cleanup, err := normalizeFinalJobVideoBackground(context.Background(), plan)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	defer cleanup()

	if normalized.Source == nil || normalized.Source.LocalPath == "" || normalized.Source.SHA256 == "" {
		t.Fatalf("normalized plan lost its source identity: %+v", normalized.Source)
	}
	if !strings.HasPrefix(normalized.Source.AssetID, "finaljob-video-") {
		t.Fatalf("normalized asset id = %q, want finaljob-video-*", normalized.Source.AssetID)
	}
	assertZeroPTS(t, ffprobe, normalized.Source.LocalPath)
	if codec := videoCodec(t, ffprobe, normalized.Source.LocalPath); codec != "h264" {
		t.Fatalf("normalized codec = %q, want h264 (stream copy preserves it)", codec)
	}
}

// TestNormalizeFinalJobVideoBackground_ReencodesNonCanonicalCodec pins the
// fallback: a source that is not already H.264/yuv420p is transcoded, and the
// transcoded output still starts at zero.
func TestNormalizeFinalJobVideoBackground_ReencodesNonCanonicalCodec(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpegTools(t)
	dir := t.TempDir()
	source := makeOffsetSource(t, ffmpeg, dir, "mpeg4")
	if pts := firstVideoPTS(t, ffprobe, source); pts <= 0 {
		t.Fatalf("fixture must start at a positive PTS, got %v", pts)
	}

	plan := capabilityoverlay.OverlayPlan{Source: &capabilityoverlay.OverlaySource{AssetID: "drive-background-06", LocalPath: source}}
	normalized, cleanup, err := normalizeFinalJobVideoBackground(context.Background(), plan)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	defer cleanup()

	assertZeroPTS(t, ffprobe, normalized.Source.LocalPath)
	if codec := videoCodec(t, ffprobe, normalized.Source.LocalPath); codec != "h264" {
		t.Fatalf("normalized codec = %q, want h264 (fallback must transcode)", codec)
	}
}
