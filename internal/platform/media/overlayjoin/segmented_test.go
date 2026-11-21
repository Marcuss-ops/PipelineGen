package overlayjoin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// TestSegmentedAssemblyMatchesSinglePass is the acceptance proof for the
// memory fix: the segmented path must emit the same pixels as the single graph.
// The comparison is framemd5 of the decoded output, not a size or duration
// check, so a one-frame shift at a segment boundary fails the test.
func TestSegmentedAssemblyMatchesSinglePass(t *testing.T) {
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is not available", binary)
		}
	}
	dir := t.TempDir()
	spec := smallTimelineSpec(t, dir, 8, 500*time.Millisecond)
	spec.OutputPath = filepath.Join(dir, "segmented.mp4")

	oldThreshold, oldDuration := segmentedInputThreshold, segmentDuration
	defer func() { segmentedInputThreshold, segmentDuration = oldThreshold, oldDuration }()

	segmentedInputThreshold = 2
	segmentDuration = time.Second
	if _, err := Run(context.Background(), spec, ""); err != nil {
		t.Fatalf("segmented Run: %v", err)
	}

	segmentedInputThreshold = 1000
	spec.OutputPath = filepath.Join(dir, "single.mp4")
	if _, err := Run(context.Background(), spec, ""); err != nil {
		t.Fatalf("single Run: %v", err)
	}

	got := decodedFrameDigest(t, filepath.Join(dir, "segmented.mp4"))
	want := decodedFrameDigest(t, filepath.Join(dir, "single.mp4"))
	if got != want {
		t.Fatalf("segmented frames differ from the single graph:\n segmented %s\n single    %s", got, want)
	}
}

func TestSegmentArgsTrimASegmentCrossingClip(t *testing.T) {
	dir := t.TempDir()
	clip := touch(t, dir, "clip.mp4")
	spec := Spec{
		AudioPath:  touch(t, dir, "audio.m4a"),
		OutputPath: filepath.Join(dir, "out.mp4"),
		Width:      320, Height: 180, FPS: 24,
		Encoder:    mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23},
		DurationUS: 3_000_000,
		Clips:      []Clip{{Path: clip, StartUS: 800_000, DurationUS: 1_000_000}},
	}
	// The clip runs 0.8 s -> 1.8 s; the segment starts at 1.0 s, so 0.2 s of the
	// clip belong to the previous segment and the overlay starts at local 0.
	// 0.2 s is frame 4.8 at 24 fps, and overlay shows floor(4.8) = frame 4 in the
	// continuous graph, so the trim starts on that frame boundary (0.166667 s).
	args := segmentArgs(spec, filepath.Join(dir, "bg.mov"), 1_000_000, 2_000_000, filepath.Join(dir, "part.mov"))
	graph := args[indexOf(args, "-filter_complex")+1]
	for _, want := range []string{
		"trim=start=0.166667:duration=0.800000",
		"setpts=PTS-STARTPTS+0.000000/TB",
		"enable='between(t,0.000000,0.800000)'",
	} {
		if !strings.Contains(graph, want) {
			t.Fatalf("segment graph missing %q:\n%s", want, graph)
		}
	}
	if got := args[indexOf(args, "-t")+1]; got != "1.000000" {
		t.Fatalf("segment length = %q, want 1.000000", got)
	}
}

func smallTimelineSpec(t *testing.T, dir string, clips int, window time.Duration) Spec {
	t.Helper()
	bg := filepath.Join(dir, "bg.mp4")
	audio := filepath.Join(dir, "audio.m4a")
	duration := time.Duration(clips) * window
	mustFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=2", "-pix_fmt", "yuv420p", bg)
	mustFFmpeg(t, "-f", "lavfi", "-i", "sine=frequency=440:duration=10", "-c:a", "aac", audio)
	spec := Spec{
		BackgroundPath: bg,
		AudioPath:      audio,
		OutputPath:     filepath.Join(dir, "out.mp4"),
		Width:          320, Height: 180, FPS: 24,
		Encoder:    mediaexec.EncoderPolicy{Codec: kernelmedia.CanonicalVideoCodec, Preset: kernelmedia.CanonicalVideoPreset, CRF: kernelmedia.CanonicalVideoCRF},
		DurationUS: int64(duration / time.Microsecond),
	}
	for i := 0; i < clips; i++ {
		clip := filepath.Join(dir, fmt.Sprintf("clip-%02d.mp4", i))
		mustFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=1",
			"-vf", fmt.Sprintf("hue=h=%d", (i*40)%360), "-pix_fmt", "yuv420p", clip)
		spec.Clips = append(spec.Clips, Clip{
			Path:       clip,
			StartUS:    int64(time.Duration(i) * window / time.Microsecond),
			DurationUS: int64(window / time.Microsecond),
		})
	}
	return spec
}

func decodedFrameDigest(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-f", "framemd5", "-").Output()
	if err != nil {
		t.Fatalf("framemd5 %s: %v", path, err)
	}
	sum := sha256.Sum256(out)
	return hex.EncodeToString(sum[:])
}
