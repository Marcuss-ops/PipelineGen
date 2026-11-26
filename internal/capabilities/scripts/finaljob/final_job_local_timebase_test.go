package finaljob

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// TestCanonicalizeFinalJobRenderedClipTimeBase pins the final-job boundary:
// a localized render may carry x264's 1/12288 track timebase, but the remote
// packet-copy lane needs 1/90000. The rewrite must change only the container
// timescale, keep the audio stream, and be idempotent.
func TestCanonicalizeFinalJobRenderedClipTimeBase(t *testing.T) {
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is not installed", binary)
		}
	}
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	generate := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "2",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", source,
	)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate fixture: %v: %s", err, output)
	}
	low := filepath.Join(dir, "localized-12288.mp4")
	remux := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", source,
		"-map", "0:v:0", "-map", "0:a:0?", "-c", "copy",
		"-video_track_timescale", "12288", low,
	)
	if output, err := remux.CombinedOutput(); err != nil {
		t.Fatalf("create 1/12288 fixture: %v: %s", err, output)
	}
	before, err := finalJobVideoTimeBase(ctx, low)
	if err != nil {
		t.Fatal(err)
	}
	if before != "1/12288" {
		t.Fatalf("fixture timebase = %s, want 1/12288", before)
	}
	beforeSHA, _, err := digest.SHA256File(low)
	if err != nil {
		t.Fatal(err)
	}

	sha, size, changed, err := canonicalizeFinalJobRenderedClipTimeBase(ctx, low)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if !changed {
		t.Fatal("a 1/12288 clip was not rewritten")
	}
	if sha == beforeSHA {
		t.Fatal("normalized bytes kept the old digest")
	}
	if size <= 0 {
		t.Fatalf("normalized size = %d", size)
	}
	after, err := finalJobVideoTimeBase(ctx, low)
	if err != nil {
		t.Fatal(err)
	}
	if after != "1/90000" {
		t.Fatalf("normalized timebase = %s, want 1/90000", after)
	}
	audio, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=codec_name",
		"-of", "default=noprint_wrappers=1:nokey=1", low,
	).Output()
	if err != nil {
		t.Fatalf("probe normalized audio: %v", err)
	}
	if string(audio) != "aac\n" {
		t.Fatalf("normalized audio codec = %q, want aac", string(audio))
	}

	sha2, size2, changed2, err := canonicalizeFinalJobRenderedClipTimeBase(ctx, low)
	if err != nil {
		t.Fatalf("canonicalize twice: %v", err)
	}
	if changed2 || sha2 != sha || size2 != size {
		t.Fatalf("second pass changed the artifact: changed=%v sha=%s/%s size=%d/%d", changed2, sha2, sha, size2, size)
	}
}
