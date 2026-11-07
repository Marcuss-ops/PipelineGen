package rustexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func TestVerifyCanonicalVideoExtradata(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	encode := func(name string, extra ...string) string {
		path := filepath.Join(dir, name)
		args := []string{
			"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
			"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24", "-frames:v", "48", "-an",
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-profile:v", "high", "-level:v", "4.0", "-pix_fmt", "yuv420p", "-r", "24",
			"-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709", "-color_range", "tv",
			"-g", "48", "-keyint_min", "48", "-sc_threshold", "0", "-bf", "0", "-refs", "1", "-flags", "+cgop",
			"-video_track_timescale", "90000", "-map_metadata", "-1", "-movflags", "+faststart",
		}
		args = append(args, extra...)
		args = append(args, path)
		if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
		return path
	}

	canonical := encode("canonical.mp4")
	if err := kernelmedia.VerifyCanonicalVideoExtradata(canonical); err != nil {
		t.Fatalf("canonical stock clip refused: %v", err)
	}

	drifted := encode("drifted.mp4", "-refs", "3", "-level:v", "4.1")
	if err := kernelmedia.VerifyCanonicalVideoExtradata(drifted); !errors.Is(err, kernelmedia.ErrVideoExtradataNotCanonical) {
		t.Fatalf("drifted SPS/PPS gate = %v, want kernelmedia.ErrVideoExtradataNotCanonical", err)
	}
}

func TestCanonicalFragmentsJoinAndDecode(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed")
	}
	dir := t.TempDir()
	encode := func(name, source string, extra ...string) string {
		path := filepath.Join(dir, name)
		args := []string{
			"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
			"-f", "lavfi", "-i", source, "-frames:v", "48", "-an",
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-profile:v", "high", "-level:v", "4.0", "-pix_fmt", "yuv420p", "-r", "24",
			"-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709", "-color_range", "tv",
			"-g", "48", "-keyint_min", "48", "-sc_threshold", "0", "-bf", "0", "-refs", "1", "-flags", "+cgop",
			"-video_track_timescale", "90000", "-map_metadata", "-1", "-movflags", "+faststart",
		}
		args = append(args, extra...)
		args = append(args, path)
		if out, runErr := exec.Command(ffmpeg, args...).CombinedOutput(); runErr != nil {
			t.Fatalf("encode %s: %v: %s", name, runErr, out)
		}
		return path
	}
	first := encode("first.mp4", "testsrc2=size=1920x1080:rate=24")
	second := encode("second.mp4", "testsrc=size=1920x1080:rate=24")
	for _, path := range []string{first, second} {
		if err := kernelmedia.VerifyCanonicalVideoExtradata(path); err != nil {
			t.Fatalf("fragment %s is not packet-copy safe: %v", filepath.Base(path), err)
		}
	}
	list := filepath.Join(dir, "concat.txt")
	if err := os.WriteFile(list, []byte(fmt.Sprintf("file '%s'\nfile '%s'\n", first, second)), 0o600); err != nil {
		t.Fatal(err)
	}
	joined := filepath.Join(dir, "joined.mp4")
	if out, runErr := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "concat", "-safe", "0", "-i", list, "-c", "copy", "-movflags", "+faststart", joined).CombinedOutput(); runErr != nil {
		t.Fatalf("join canonical fragments: %v: %s", runErr, out)
	}
	if err := kernelmedia.VerifyCanonicalVideoExtradata(joined); err != nil {
		t.Fatalf("joined output lost the canonical bitstream: %v", err)
	}
	probeOut, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0", "-count_frames",
		"-show_entries", "stream=nb_read_frames", "-of", "default=nokey=1:noprint_wrappers=1", joined).Output()
	if err != nil {
		t.Fatalf("count joined frames: %v", err)
	}
	if got := strings.TrimSpace(string(probeOut)); got != "96" {
		t.Fatalf("joined frame count = %q, want 96", got)
	}
	// Decode the whole join, not just its header: a parameter-set mismatch
	// surfaces at the junction, where the decoder applies the first segment's
	// SPS/PPS to the second segment's slices.
	if out, runErr := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", joined, "-f", "null", "-").CombinedOutput(); runErr != nil || len(strings.TrimSpace(string(out))) != 0 {
		t.Fatalf("decode across the join: %v: %s", runErr, out)
	}
	drifted := encode("drifted.mp4", "testsrc2=size=1920x1080:rate=24", "-refs", "3", "-level:v", "4.1")
	if err := kernelmedia.VerifyCanonicalVideoExtradata(drifted); !errors.Is(err, kernelmedia.ErrVideoExtradataNotCanonical) {
		t.Fatalf("drifted fragment gate = %v, want kernelmedia.ErrVideoExtradataNotCanonical", err)
	}
}
