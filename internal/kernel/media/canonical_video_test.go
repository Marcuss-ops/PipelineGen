package media

import (
	"strings"
	"testing"
)

// TestCanonicalVideoEncoderArgsPinTheBitstreamIdentity keeps the shared recipe
// honest: every setting that lands in the SPS/PPS/VUI must be present. A future
// "cleanup" that drops one of these silently changes the avcC.
func TestCanonicalVideoEncoderArgsPinTheBitstreamIdentity(t *testing.T) {
	args := CanonicalVideoEncoderArgs()
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-c:v libx264",
		"-preset veryfast",
		"-crf 23",
		"-refs 1",
		"-profile:v high",
		"-level:v 4.0",
		"-pix_fmt yuv420p",
		"-g 48",
		"-keyint_min 48",
		"-sc_threshold 0",
		"-bf 0",
		"-flags +cgop",
		"-x264-params " + CanonicalVideoX264Params,
		"-colorspace bt709",
		"-color_primaries bt709",
		"-color_trc bt709",
		"-color_range tv",
		"-video_track_timescale 90000",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("canonical encoder args are missing %q:\n%s", want, joined)
		}
	}
}

func TestIsCanonicalVideoEncoder(t *testing.T) {
	if !IsCanonicalVideoEncoder(" libx264 ", " VERYFAST ", 23) {
		t.Fatal("the canonical lane must be recognised despite case/whitespace")
	}
	for _, tc := range []struct {
		name   string
		codec  string
		preset string
		crf    int
	}{
		{"nvenc", "h264_nvenc", "p1", 23},
		{"different preset", "libx264", "medium", 23},
		{"different quality", "libx264", "veryfast", 20},
		{"empty", "", "", 0},
	} {
		if IsCanonicalVideoEncoder(tc.codec, tc.preset, tc.crf) {
			t.Errorf("%s must not be accepted as the canonical lane", tc.name)
		}
	}
}
