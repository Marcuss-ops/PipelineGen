package rustexec

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/adminmedia"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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

func TestNormalizeHonorsDisableDuration(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"normalize"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	processor := &VideoProcessor{client: client}

	opts := ffmpeg.NormalizeOptions{Duration: 30, DisableDuration: true, KeepAudio: true}
	if err := processor.Normalize(context.Background(), "in.mp4", "out.mp4", opts); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.DurationSec != 0 || !sent.KeepAudio {
		t.Fatalf("unexpected normalize request: %+v", sent)
	}
}

func TestStockRendererSendsTypedRenderCapability(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"render_stock"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	renderer := &StockRenderer{client: client}
	_, err := renderer.Render(context.Background(), stockpipeline.RenderRequest{
		InputPaths: []string{"a.mp4", "b.mp4"}, OutputPath: "out.mp4",
		Codec: "h264_nvenc", Preset: "p1", CRF: 23, Width: 1920, Height: 1080, FPS: 24,
		TransitionEvery: 2, ClipDurationSec: 5, EffectEvery: 3, EffectIndexHint: 1,
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Operation != "render_stock" || len(sent.InputPaths) != 2 || sent.Codec != "h264_nvenc" || sent.TransitionEvery != 2 {
		t.Fatalf("unexpected render request: %+v", sent)
	}
}

func TestAdminRendererPreservesEncoderPolicy(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"admin_render"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	processor := &AdminMediaProcessor{client: client, policy: config.VideoEncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 21}}
	manifest := adminmedia.RenderManifest{
		Input: "in.mp4", Output: "out.mp4", Font: "/tmp/font.ttf",
		Effects:  []adminmedia.RenderEffect{{Path: "fx.mp4", DelayMS: 10, Duration: 1, Volume: "0.5"}},
		Overlays: []adminmedia.RenderOverlay{{Text: "hello", Start: "0", End: "1", Size: "32", Y: "10", Color: "white"}},
	}
	if err := processor.Render(context.Background(), manifest); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Operation != "admin_render" || sent.Codec != "h264_nvenc" || sent.Preset != "p1" || sent.CRF != 21 || len(sent.Effects) != 1 || len(sent.Overlays) != 1 {
		t.Fatalf("unexpected admin render request: %+v", sent)
	}
}
