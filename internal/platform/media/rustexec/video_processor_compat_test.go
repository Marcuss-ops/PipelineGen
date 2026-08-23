package rustexec

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
)

func TestNormalizeUsesLegacyScalarOverrides(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"normalize"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	processor := &VideoProcessor{
		client:  client,
		policy:  mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 23},
		profile: mediaexec.VideoProfile{}.WithDefaults(),
	}

	opts := mediaexec.NormalizeOptions{
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, KeyframeInterval: 60,
		Codec: "libx264", Preset: "slow", CRF: 20,
		KeepAudio: true,
	}
	if err := processor.Normalize(context.Background(), "in.mp4", "out.mp4", opts); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Width != 1280 || sent.Height != 720 || sent.FPSNum != 30 || sent.FPSDen != 1 || sent.KeyframeInterval != 60 || sent.Codec != "libx264" || sent.Preset != "slow" || sent.CRF != 20 {
		t.Fatalf("legacy scalar overrides were not applied: %+v", sent)
	}
}

func TestCutAndNormalizeUsesCanonicalPolicyWhenScalarsAreEmpty(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"cut_and_normalize"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	processor := &VideoProcessor{client: client, profile: mediaexec.VideoProfile{}.WithDefaults()}

	opts := mediaexec.CutAndNormalizeOptions{
		Policy: mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 19},
	}
	if err := processor.CutAndNormalize(context.Background(), "in.mp4", "out.mp4", "0", "2", opts); err != nil {
		t.Fatalf("CutAndNormalize() error = %v", err)
	}

	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Codec != "h264_nvenc" || sent.Preset != "p1" || sent.CRF != 19 {
		t.Fatalf("canonical policy was not applied: %+v", sent)
	}
}

func TestCutAndNormalizeUsesLegacyScalarOverrides(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"cut_and_normalize"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	processor := &VideoProcessor{
		client:  client,
		policy:  mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 23},
		profile: mediaexec.VideoProfile{}.WithDefaults(),
	}

	opts := mediaexec.CutAndNormalizeOptions{
		Width: 1024, Height: 576, FPSNum: 25, FPSDen: 1,
		Codec: "libx265", Preset: "medium", CRF: 22,
	}
	if err := processor.CutAndNormalize(context.Background(), "in.mp4", "out.mp4", "0", "2", opts); err != nil {
		t.Fatalf("CutAndNormalize() error = %v", err)
	}

	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Width != 1024 || sent.Height != 576 || sent.FPSNum != 25 || sent.FPSDen != 1 || sent.Codec != "libx265" || sent.Preset != "medium" || sent.CRF != 22 {
		t.Fatalf("legacy scalar overrides were not applied: %+v", sent)
	}
}
