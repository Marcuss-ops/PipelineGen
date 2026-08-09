package rustexec

import (
	"context"
	"encoding/json"
	"testing"

	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
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
