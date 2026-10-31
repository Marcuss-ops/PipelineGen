package wiring

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func TestNVENCPresetForMapsX264PresetNames(t *testing.T) {
	cases := map[string]string{
		"ultrafast": "p2", "superfast": "p2", "veryfast": "p2", "faster": "p2",
		"fast": "p4", "medium": "p4", "slow": "p4",
		"slower": "p6", "veryslow": "p6", "p5": "p5",
	}
	for input, want := range cases {
		if got := nvencPresetFor(input); got != want {
			t.Errorf("nvencPresetFor(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFinalJobSegmentPreparerEncodesOneAssemblyReadyFragment(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	output := filepath.Join(dir, "fragment.mp4")
	create := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-t", "2",
		"-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", source,
	)
	if out, runErr := create.CombinedOutput(); runErr != nil {
		t.Fatalf("make source fixture: %v: %s", runErr, out)
	}
	preparer := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23})
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-0000", SourceAssetID: "source-1", SourceSHA256: "sha",
		SourceInUS: 0, SourceDurationUS: 2_000_000, TimelineStartFrame: 0, FrameCount: 48,
		TimelineRevision: 1, TimelineSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := preparer.encodeFragment(context.Background(), source, output, request); err != nil {
		t.Fatalf("encodeFragment: %v", err)
	}
	if err := verifyFinalJobFragment(context.Background(), output, request.FrameCount); err != nil {
		t.Fatalf("verifyFinalJobFragment: %v", err)
	}
}

func TestFinalJobSegmentPreparerPadsShortBoundaryToExactFrameCount(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "short-source.mp4")
	output := filepath.Join(dir, "fragment.mp4")
	create := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-frames:v", "65",
		"-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", source,
	)
	if out, runErr := create.CombinedOutput(); runErr != nil {
		t.Fatalf("make short source fixture: %v: %s", runErr, out)
	}
	preparer := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23})
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-boundary", SourceAssetID: "source-short", SourceSHA256: "sha",
		SourceInUS: 0, SourceDurationUS: 2_750_000, TimelineStartFrame: 65, FrameCount: 66,
		TimelineRevision: 1, TimelineSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := preparer.encodeFragment(context.Background(), source, output, request); err != nil {
		t.Fatalf("encodeFragment: %v", err)
	}
	if err := verifyFinalJobFragment(context.Background(), output, request.FrameCount); err != nil {
		t.Fatalf("verifyFinalJobFragment: %v", err)
	}
}
