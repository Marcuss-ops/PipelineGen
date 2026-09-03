// Package media — enrichment_adapters_test.go: FFMPEGKeyframeSamplerAdapter
// certification. Uses the REAL ffmpeg/ffprobe binaries (available in CI and
// dev) against a tiny generated video to pin two invariants:
//
//  1. A percentage of 100% (EOF seek) still yields a decodable frame — the
//     adapter must clamp the seek strictly inside the stream instead of
//     exiting silently with no output file (the p=1.0 case ships in
//     uniformPercentages, so this is the common path, not an edge case).
//  2. A seek that produces no output file must FAIL CLOSED — a missing
//     file must never be reported as a sample (previously ffmpeg exited 0
//     on EOF seeks and the adapter propagated the phantom path).
package media_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// requireFFMPEG skips the test when the ffmpeg/ffprobe binaries are not
// installed (mirrors the convention of the live-ffmpeg legs elsewhere in
// the package: the certification environment always provides them).
func requireFFMPEG(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not in PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not in PATH")
	}
}

// writeTestVideo generates a 2-second test video via ffmpeg's lavfi
// color source so the sampler tests exercise the real demux/decode path.
func writeTestVideo(t *testing.T, path string) {
	t.Helper()
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:s=64x64:d=2",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-y", path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate test video: %v: %s", err, out)
	}
}

func TestFFMPEGKeyframeSampler_EOFPercentageStillYieldsFrame(t *testing.T) {
	requireFFMPEG(t)
	dir := t.TempDir()
	video := filepath.Join(dir, "in.mp4")
	writeTestVideo(t, video)

	sampler, err := pgmedia.NewFFMPEGKeyframeSamplerAdapter("")
	if err != nil {
		t.Fatalf("sampler: %v", err)
	}
	outDir := t.TempDir()
	// Exactly the percentages uniformPercentages(5) produces, including
	// the EOF boundary p=1.0 that previously produced a phantom frame.
	frames, err := sampler.ExtractPercentageFrames(context.Background(), video,
		[]float64{0, 0.25, 0.5, 0.75, 1}, outDir)
	if err != nil {
		t.Fatalf("extract frames: %v", err)
	}
	if len(frames) != 5 {
		t.Fatalf("got %d frames, want 5", len(frames))
	}
	for _, f := range frames {
		if _, err := os.Stat(f.Path); err != nil {
			t.Fatalf("frame %q missing: %v", f.Path, err)
		}
	}
}

func TestFFMPEGKeyframeSampler_FailClosedOnMissingOutput(t *testing.T) {
	requireFFMPEG(t)
	dir := t.TempDir()
	video := filepath.Join(dir, "in.mp4")
	writeTestVideo(t, video)

	sampler, err := pgmedia.NewFFMPEGKeyframeSamplerAdapter("")
	if err != nil {
		t.Fatalf("sampler: %v", err)
	}
	// A stream of zero frames (f "color=...:d=0" is invalid, so use a
	// truncated file): cutting the container to zero frames makes every
	// seek decode nothing. The adapter must fail rather than return a
	// path to a file that does not exist.
	truncated := filepath.Join(dir, "truncated.mp4")
	raw, err := os.ReadFile(video)
	if err != nil {
		t.Fatalf("read video: %v", err)
	}
	if err := os.WriteFile(truncated, raw[:len(raw)/4], 0o644); err != nil {
		t.Fatalf("write truncated video: %v", err)
	}
	if _, err := sampler.ExtractPercentageFrames(context.Background(), truncated,
		[]float64{0}, t.TempDir()); err == nil {
		t.Fatal("expected fail-closed error for a video with no decodable frames, got nil")
	}
}
