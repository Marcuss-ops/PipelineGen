package render

import (
	"context"
	"os"
	"strings"
	"testing"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"go.uber.org/zap"
)

type fakeRustMusclesRunner struct {
	output []byte
	err    error
}

func (f fakeRustMusclesRunner) Run(context.Context, string, []byte) ([]byte, error) {
	return f.output, f.err
}

func TestRustCutterMapsSuccessfulResponse(t *testing.T) {
	t.Parallel()
	out := t.TempDir() + "/clip.mp4"
	if err := os.WriteFile(out, []byte("clip"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := fakeRustMusclesRunner{output: []byte(`{"ok":true,"operation":"cut_batch","source_path":"source.mp4","items":[{"job_id":"` + out + `","output_path":"` + out + `","status":"validated","size_bytes":4,"duration_sec":1}]}`)}
	cutter := NewRustCutter("ignored", "ffmpeg", zap.NewNop())
	cutter.runner = runner

	result, err := cutter.Cut(context.Background(), stockpipeline.CutRequest{
		SourcePath: "source.mp4",
		Jobs:       []stockpipeline.CutJob{{StartSec: 1, EndSec: 2, OutputPath: out}},
	})
	if err != nil {
		t.Fatalf("Cut() error = %v", err)
	}
	if got := result.Items[0].Status; got != stockpipeline.CutItemStatusValidated {
		t.Fatalf("status = %v, want validated", got)
	}
	if result.Items[0].SHA256Hex == "" || result.Items[0].SizeBytes != 4 {
		t.Fatalf("result did not validate output: %+v", result.Items[0])
	}
}

func TestRustCutterRejectsMalformedResponse(t *testing.T) {
	t.Parallel()
	cutter := NewRustCutter("ignored", "ffmpeg", zap.NewNop())
	cutter.runner = fakeRustMusclesRunner{output: []byte("not-json")}
	_, err := cutter.Cut(context.Background(), stockpipeline.CutRequest{})
	if err == nil || !strings.Contains(err.Error(), "decode rust cut response") {
		t.Fatalf("error = %v, want malformed response error", err)
	}
}
