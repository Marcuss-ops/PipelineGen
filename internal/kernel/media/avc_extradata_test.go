package media

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestVerifyCanonicalVideoExtradata pins the publication gate on the bytes.
// The canonical fixture is encoded with the same effective profile the stock
// normalization pass and the fragment producer pin (libx264 veryfast, High
// 4.0, ref=1, fixed closed GOP, bt709 VUI); the drifted fixture keeps the same
// codec name, profile and geometry but changes the reference count and level,
// which is exactly the class the nominal profile checks cannot see.
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
	if err := VerifyCanonicalVideoExtradata(canonical); err != nil {
		t.Fatalf("canonical stock clip refused: %v", err)
	}

	drifted := encode("drifted.mp4", "-refs", "3", "-level:v", "4.1")
	if err := VerifyCanonicalVideoExtradata(drifted); !errors.Is(err, ErrVideoExtradataNotCanonical) {
		t.Fatalf("drifted SPS/PPS gate = %v, want ErrVideoExtradataNotCanonical", err)
	}
}
