package rustexec

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
)

type fakeRunner struct {
	stdout []byte
	stderr []byte
	err    error
	input  []byte
}

func (f *fakeRunner) Run(_ context.Context, _ string, input []byte) ([]byte, []byte, error) {
	f.input = input
	return f.stdout, f.stderr, f.err
}

func TestClientProbeMapsRustMetadata(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"probe","metadata":{"duration_sec":12.5,"width":1920,"height":1080,"fps":24,"video_codec":"h264","audio_codec":"aac","sample_rate":48000,"channels":2,"has_video":true,"has_audio":true}}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner

	got, err := (&VideoProcessor{client: client}).Probe(context.Background(), "/tmp/input.mp4")
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if got.Duration.Seconds() != 12.5 || got.Width != 1920 || got.Height != 1080 || got.VideoCodec != "h264" {
		t.Fatalf("unexpected metadata: %+v", got)
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Operation != "probe" || sent.SourcePath != "/tmp/input.mp4" || sent.FFmpegPath != "ffmpeg" {
		t.Fatalf("unexpected request: %+v", sent)
	}
}

func TestApplyWatermarkForwardsScaleAndGreenScreen(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"watermark"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	processor := &VideoProcessor{
		client:  client,
		profile: mediaexec.VideoProfile{}.WithDefaults(),
		policy:  mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23},
	}

	err := processor.ApplyWatermark(context.Background(), "in.mp4", "out.mp4", mediaexec.WatermarkOptions{
		ImagePath:             "wm.png",
		Opacity:               0.25,
		Position:              "center",
		ScalePercent:          20,
		GreenScreenColor:      "0x00FF00",
		GreenScreenSimilarity: 0.3,
		GreenScreenBlend:      0.1,
	})
	if err != nil {
		t.Fatalf("ApplyWatermark() error = %v", err)
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Operation != OperationWatermark {
		t.Fatalf("operation = %q, want watermark", sent.Operation)
	}
	if sent.OverlayPath != "wm.png" || sent.Opacity != 0.25 {
		t.Fatalf("overlay fields = %q/%v, want wm.png/0.25", sent.OverlayPath, sent.Opacity)
	}
	if sent.ScalePercent != 20 {
		t.Errorf("ScalePercent = %d, want 20", sent.ScalePercent)
	}
	if sent.GreenScreenColor != "0x00FF00" {
		t.Errorf("GreenScreenColor = %q, want 0x00FF00", sent.GreenScreenColor)
	}
	if sent.GreenScreenSimilarity != 0.3 {
		t.Errorf("GreenScreenSimilarity = %v, want 0.3", sent.GreenScreenSimilarity)
	}
	if sent.GreenScreenBlend != 0.1 {
		t.Errorf("GreenScreenBlend = %v, want 0.1", sent.GreenScreenBlend)
	}
}

func TestApplyWatermarkOmitsScaleAndGreenScreenWhenUnset(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"watermark"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	processor := &VideoProcessor{
		client:  client,
		profile: mediaexec.VideoProfile{}.WithDefaults(),
		policy:  mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23},
	}

	err := processor.ApplyWatermark(context.Background(), "in.mp4", "out.mp4", mediaexec.WatermarkOptions{
		ImagePath: "wm.png",
		Opacity:   1.0,
	})
	if err != nil {
		t.Fatalf("ApplyWatermark() error = %v", err)
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.ScalePercent != 0 {
		t.Errorf("ScalePercent = %d, want 0 when unset", sent.ScalePercent)
	}
	if sent.GreenScreenColor != "" {
		t.Errorf("GreenScreenColor = %q, want empty when unset", sent.GreenScreenColor)
	}
}

func TestNormalizeHonorsDisableDuration(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"normalize"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	processor := &VideoProcessor{client: client, profile: mediaexec.VideoProfile{}.WithDefaults()}

	opts := mediaexec.NormalizeOptions{
		Duration: 30, DisableDuration: true, KeepAudio: true,
		Policy: mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 23},
	}
	if err := processor.Normalize(context.Background(), "in.mp4", "out.mp4", opts); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.DurationSec != 0 || !sent.KeepAudio || sent.Width != 1920 || sent.Height != 1080 || sent.FPSNum != 24 || sent.FPSDen != 1 || sent.KeyframeInterval != 48 || sent.AudioCodec != "aac" || sent.AudioBitrate != "192k" || sent.SampleRate != 48000 || sent.Channels != 2 {
		t.Fatalf("unexpected fully resolved normalize request: %+v", sent)
	}
}

func TestVideoProcessorCutUsesSharedProtocolAndConfiguredPolicy(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	out := filepath.Join(t.TempDir(), "clip.mp4")
	// The stock cutter accepts only the canonical libx264 lane, and the
	// bitstream gate reads the real SPS/PPS: the fixture must therefore be a
	// canonical assembly clip, not a placeholder file.
	fixture := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24", "-frames:v", "24", "-an",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-profile:v", "high", "-level:v", "4.0", "-pix_fmt", "yuv420p", "-r", "24",
		"-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709", "-color_range", "tv",
		"-g", "48", "-keyint_min", "48", "-sc_threshold", "0", "-bf", "0", "-refs", "1", "-flags", "+cgop",
		"-video_track_timescale", "90000", "-map_metadata", "-1", "-movflags", "+faststart", out,
	}
	if outBytes, err := exec.Command(ffmpeg, fixture...).CombinedOutput(); err != nil {
		t.Fatalf("create canonical cut fixture: %v: %s", err, outBytes)
	}
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"cut_batch","items":[{"job_id":"` + out + `","output_path":"` + out + `","status":"validated","size_bytes":4,"duration_sec":1}]}`)}
	processor := NewConfiguredVideoProcessor("muscles", "ffmpeg", mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}, mediaexec.VideoProfile{}.WithDefaults(), nil)
	processor.client.runner = runner

	result, err := processor.Cut(context.Background(), stockpipeline.CutRequest{
		SourcePath: "source.mp4",
		Jobs:       []stockpipeline.CutJob{{StartSec: 1, EndSec: 2, OutputPath: out}},
		Width:      1920, Height: 1080, FPSNum: 24, FPSDen: 1, KeyframeInterval: 48,
	})
	if err != nil {
		t.Fatalf("Cut() error = %v", err)
	}
	if result.Items[0].Status != stockpipeline.CutItemStatusValidated {
		t.Fatalf("unexpected cut result: %+v", result.Items[0])
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Operation != "cut_batch" || sent.Codec != "libx264" || sent.Preset != "veryfast" || sent.Width != 1920 || sent.Height != 1080 || sent.FPSNum != 24 || sent.FPSDen != 1 || sent.KeyframeInterval != 48 || sent.AudioCodec != "aac" || sent.AudioBitrate != "192k" || sent.SampleRate != 48000 || sent.Channels != 2 || len(sent.Jobs) != 1 {
		t.Fatalf("unexpected shared cut request: %+v", sent)
	}
}

func TestVideoProcessorEncodingFailsWithoutPolicy(t *testing.T) {
	processor := &VideoProcessor{client: NewClient("muscles", "ffmpeg", nil), profile: mediaexec.VideoProfile{}.WithDefaults()}
	err := processor.Normalize(context.Background(), "in.mp4", "out.mp4", mediaexec.NormalizeOptions{})
	if err == nil || err.Error() != "ENCODER_POLICY_REQUIRED: Go did not provide a complete video encoder policy" {
		t.Fatalf("Normalize() error = %v, want missing policy error", err)
	}
}
