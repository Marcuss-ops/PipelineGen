package finaljob

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	segment "github.com/Marcuss-ops/PipelineGen/internal/kernel/media/finaljob"
)

// TestFragmentEncoderRewritesLegacyColourVUIToCanonicalAVCC is the regression
// for the overlay recovery lane. A legacy overlay may carry the encoder's sRGB
// transfer tag (iec61966-2-1) instead of bt709; re-encoding it must overwrite
// the VUI, not inherit it, or the fragment cannot be certified for packet-copy
// assembly.
//
// The preparer is deliberately constructed with an NVENC policy: the lane must
// pin the canonical x264 encoder instead of inheriting the user-facing render
// policy from config.production (which selects h264_nvenc).
func TestFragmentEncoderRewritesLegacyColourVUIToCanonicalAVCC(t *testing.T) {
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is not installed", binary)
		}
	}
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "legacy-overlay.mp4")
	generate := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=1920x1080:rate=24",
		"-frames:v", "48",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "30",
		"-pix_fmt", "yuv420p",
		"-colorspace", "bt709", "-color_primaries", "bt709",
		"-color_trc", "iec61966-2-1", "-color_range", "tv",
		"-an", source,
	)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate legacy overlay: %v: %s", err, output)
	}
	before, err := probeFinalJobFragment(ctx, source)
	if err != nil {
		t.Fatalf("probe legacy overlay: %v", err)
	}
	if strings.EqualFold(before.extradataSHA256, kernelmedia.CanonicalVideoExtradataSHA256) {
		t.Fatal("fixture unexpectedly already carries the canonical avcC")
	}

	preparer := NewSegmentPreparer("", kernelmedia.EncoderPolicy{
		Codec: "h264_nvenc", Preset: "p1", CRF: 23,
	}, "", 1)
	if !kernelmedia.IsCanonicalVideoEncoder(preparer.policy.Codec, preparer.policy.Preset, preparer.policy.CRF) {
		t.Fatalf("preparer policy = %s/%s/%d, want the canonical libx264/veryfast/23",
			preparer.policy.Codec, preparer.policy.Preset, preparer.policy.CRF)
	}

	output := filepath.Join(dir, "fragment.mp4")
	request := segment.VideoSegmentRequest{
		SegmentID: "legacy-overlay-0000", FrameCount: 24,
		TimelineRevision: 1, TimelineSHA256: strings.Repeat("a", 64),
		TimelineStartFrame: 0,
	}
	if err := preparer.encodeFragment(ctx, source, output, request, false, 0); err != nil {
		t.Fatalf("encodeFragment: %v", err)
	}
	after, err := probeFinalJobFragment(ctx, output)
	if err != nil {
		t.Fatalf("probe regenerated fragment: %v", err)
	}
	if !strings.EqualFold(after.extradataSHA256, kernelmedia.CanonicalVideoExtradataSHA256) {
		t.Fatalf("regenerated avcC = %s, want the canonical %s",
			after.extradataSHA256, kernelmedia.CanonicalVideoExtradataSHA256)
	}
	if err := after.certify(request.FrameCount); err != nil {
		t.Fatalf("regenerated fragment does not certify: %v", err)
	}
}

// TestFragmentEncoderRejectsANonCanonicalPolicy pins the early gate: the
// preparer normalises the constructor policy, but an explicit policy mutation
// must still fail before ffmpeg starts instead of emitting bytes the remote mux
// will reject.
func TestFragmentEncoderRejectsANonCanonicalPolicy(t *testing.T) {
	preparer := &SegmentPreparer{policy: kernelmedia.EncoderPolicy{
		Codec: "h264_nvenc", Preset: "p1", CRF: 23,
	}}
	if _, _, err := preparer.fragmentEncoderSettings(); err == nil {
		t.Fatal("a non-canonical encoder policy must fail closed")
	}
}
