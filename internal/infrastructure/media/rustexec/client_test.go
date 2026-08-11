package rustexec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/adminmedia"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
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
	if sent.DurationSec != 0 || !sent.KeepAudio || sent.Width != 1920 || sent.Height != 1080 || sent.FPS != 24 || sent.KeyframeInterval != 48 || sent.AudioCodec != "aac" || sent.AudioBitrate != "128k" || sent.SampleRate != 48000 || sent.Channels != 2 {
		t.Fatalf("unexpected fully resolved normalize request: %+v", sent)
	}
}

func TestMuxFinalAudioCopySendsDedicatedCopyOperation(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"mux_audio_copy"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	processor := &VideoProcessor{client: client}
	audioPath := t.TempDir() + "/final_audio.m4a"
	if err := os.WriteFile(audioPath, []byte("certified"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("certified"))
	asset := capabilityaudio.FinalAudioAsset{AudioContractVersion: capabilityaudio.AudioContractVersion, AudioPlanVersion: capabilityaudio.AudioPlanVersion, AudioPlanSHA256: "plan", FinalAudioSHA256: fmt.Sprintf("%x", hash[:]), Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo", DurationMS: 1000, Bitrate: 128000, SizeBytes: 9, FinalMix: true, CopyEligible: true}
	if err := processor.MuxFinalAudioCopy(context.Background(), "/tmp/video.mp4", audioPath, "/tmp/output.mp4", asset); err != nil {
		t.Fatalf("MuxFinalAudioCopy() error = %v", err)
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Operation != OperationMuxAudioCopy || len(sent.InputPaths) != 2 {
		t.Fatalf("unexpected mux request: %+v", sent)
	}
}

func TestMuxFinalAudioCopyRejectsIncompatibleAsset(t *testing.T) {
	processor := &VideoProcessor{client: NewClient("muscles", "ffmpeg", nil)}
	err := processor.MuxFinalAudioCopy(context.Background(), "video.mp4", "audio.m4a", "out.mp4", capabilityaudio.FinalAudioAsset{})
	if !errors.Is(err, capabilityaudio.ErrAudioMediaIncompatible) {
		t.Fatalf("error = %v, want AUDIO_MEDIA_INCOMPATIBLE", err)
	}
}

func TestStockRendererSendsTypedRenderCapability(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"render_stock"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	renderer := &StockRenderer{client: client, profile: mediaexec.VideoProfile{}.WithDefaults()}
	_, err := renderer.Render(context.Background(), stockpipeline.RenderRequest{
		InputPaths: []string{"a.mp4", "b.mp4"}, OutputPath: "out.mp4",
		Codec: "h264_nvenc", Preset: "p1", CRF: 23, Width: 1920, Height: 1080, FPS: 24,
		Transitions: []stockpipeline.RenderTransition{{ClipIndex: 1, Segment: "end", ID: "fadeblack"}},
		EffectPaths: []stockpipeline.RenderEffectPath{{ClipIndex: 1, Path: "/effects/a.mp4"}}, ClipDurationSec: 5,
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Operation != "render_stock" || len(sent.InputPaths) != 2 || sent.Codec != "h264_nvenc" || sent.Width != 1920 || sent.Height != 1080 || sent.FPS != 24 || sent.KeyframeInterval != 48 || sent.AudioCodec != "aac" || sent.SampleRate != 48000 || sent.Channels != 2 || len(sent.Transitions) != 1 || sent.Transitions[0].ID != "fadeblack" || len(sent.EffectPaths) != 1 || sent.EffectPaths[0].Path != "/effects/a.mp4" {
		t.Fatalf("unexpected render request: %+v", sent)
	}
}

func TestVideoProcessorCutUsesSharedProtocolAndConfiguredPolicy(t *testing.T) {
	out := t.TempDir() + "/clip.mp4"
	if err := os.WriteFile(out, []byte("clip"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"cut_batch","items":[{"job_id":"` + out + `","output_path":"` + out + `","status":"validated","size_bytes":4,"duration_sec":1}]}`)}
	processor := NewConfiguredVideoProcessor("muscles", "ffmpeg", mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 23}, mediaexec.VideoProfile{}.WithDefaults(), nil)
	processor.client.runner = runner

	result, err := processor.Cut(context.Background(), stockpipeline.CutRequest{
		SourcePath: "source.mp4",
		Jobs:       []stockpipeline.CutJob{{StartSec: 1, EndSec: 2, OutputPath: out}},
		Width:      1920, Height: 1080, FPS: 24, KeyframeInterval: 48,
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
	if sent.Operation != "cut_batch" || sent.Codec != "h264_nvenc" || sent.Width != 1920 || sent.Height != 1080 || sent.FPS != 24 || sent.KeyframeInterval != 48 || sent.AudioCodec != "aac" || sent.AudioBitrate != "128k" || sent.SampleRate != 48000 || sent.Channels != 2 || len(sent.Jobs) != 1 {
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

func TestAdminRendererPreservesEncoderPolicy(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"admin_render"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	processor := &AdminMediaProcessor{client: client, policy: mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 21}, profile: mediaexec.VideoProfile{}.WithDefaults()}
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
	if sent.Operation != "admin_render" || sent.Codec != "h264_nvenc" || sent.Preset != "p1" || sent.CRF != 21 || sent.Width != 1920 || sent.Height != 1080 || sent.FPS != 24 || sent.KeyframeInterval != 48 || sent.AudioCodec != "aac" || sent.AudioBitrate != "128k" || sent.SampleRate != 48000 || sent.Channels != 2 || len(sent.Effects) != 1 || len(sent.Overlays) != 1 {
		t.Fatalf("unexpected admin render request: %+v", sent)
	}
}
