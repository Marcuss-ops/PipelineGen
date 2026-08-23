package youtube

import (
	"context"
	"testing"
	"time"

	youtubeapp "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
)

// fakeProbe is a scriptable MediaProbe for the adapter tests.
type fakeProbe struct {
	info *mediaexec.MediaInfo
	err  error
}

func (f *fakeProbe) Probe(_ context.Context, _ string) (*mediaexec.MediaInfo, error) {
	return f.info, f.err
}

func TestFFProbeAdapter_ValidClip(t *testing.T) {
	adapter := NewFFProbeAdapter(&fakeProbe{info: &mediaexec.MediaInfo{
		Duration:   19 * time.Second,
		Width:      1920,
		Height:     1080,
		FPS:        30,
		HasVideo:   true,
		HasAudio:   true,
		VideoCodec: "h264",
		AudioCodec: "aac",
	}})
	report, err := adapter.ValidateClip(context.Background(), "/tmp/clip.mp4", 19, true)
	if err != nil {
		t.Fatalf("ValidateClip returned error: %v", err)
	}
	if !report.ContainerReadable || !report.VideoStreamPresent || !report.AudioPresent {
		t.Fatalf("expected fully-readable report, got %+v", report)
	}
	if report.DurationSeconds != 19 {
		t.Fatalf("duration = %v, want 19", report.DurationSeconds)
	}
	if report.Width != 1920 || report.Height != 1080 || report.FPS != 30 {
		t.Fatalf("dimensions/fps wrong: %+v", report)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("valid clip should carry no warnings, got %v", report.Warnings)
	}
}

func TestFFProbeAdapter_StubClipDetected(t *testing.T) {
	// A 262-byte empty MP4 stub probes as: no video, no audio, zero
	// duration, zero dimensions — exactly what the Rust probe returns
	// for the stub artifacts observed in the Aug 2026 recovery.
	adapter := NewFFProbeAdapter(&fakeProbe{info: &mediaexec.MediaInfo{}})
	report, err := adapter.ValidateClip(context.Background(), "/tmp/stub.mp4", 19, true)
	if err != nil {
		t.Fatalf("ValidateClip returned error: %v", err)
	}
	if report.ContainerReadable != true {
		t.Fatalf("container should read (probe succeeded), got %+v", report)
	}
	if report.VideoStreamPresent {
		t.Fatal("stub must not report a video stream")
	}
	if report.AudioPresent {
		t.Fatal("stub must not report audio")
	}
	if report.DurationSeconds != 0 {
		t.Fatalf("stub duration = %v, want 0", report.DurationSeconds)
	}
	if len(report.Warnings) == 0 {
		t.Fatal("stub should carry a partial-metadata warning")
	}
	// The use case's validateFFProbeReport would reject this report:
	// no video stream → FailureCodeFFProbeValidationFailed.
	if err := validateFFProbeReportForTest(report); err == nil {
		t.Fatal("validateFFProbeReport must reject the stub report")
	}
}

func TestFFProbeAdapter_ProbeError(t *testing.T) {
	adapter := NewFFProbeAdapter(&fakeProbe{err: &probeErr{}})
	if _, err := adapter.ValidateClip(context.Background(), "/tmp/missing.mp4", 19, true); err == nil {
		t.Fatal("probe error must propagate")
	}
}

func TestFFProbeAdapter_NilProbe(t *testing.T) {
	adapter := NewFFProbeAdapter(nil)
	if _, err := adapter.ValidateClip(context.Background(), "/tmp/clip.mp4", 19, true); err == nil {
		t.Fatal("nil probe must fail closed")
	}
}

type probeErr struct{}

func (*probeErr) Error() string { return "probe failed" }

// validateFFProbeReportForTest mirrors the use-case gate without importing
// the usecase package (avoiding a dependency cycle in this adapter package).
func validateFFProbeReportForTest(report *youtubeapp.FFProbeReport) error {
	if report == nil {
		return &probeErr{}
	}
	if !report.ContainerReadable {
		return &probeErr{}
	}
	if !report.VideoStreamPresent {
		return &probeErr{}
	}
	return nil
}
