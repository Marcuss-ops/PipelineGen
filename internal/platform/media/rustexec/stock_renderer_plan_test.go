package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
)

// stockRenderRunner answers the three operations the canonical stock render
// path issues (probe → render_stock → optional mux_audio_copy) and records
// every envelope so tests can assert the transported plan and audio policy.
type stockRenderRunner struct {
	inputs   [][]byte
	hasAudio bool
}

func (r *stockRenderRunner) Run(_ context.Context, _ string, input []byte) ([]byte, []byte, error) {
	r.inputs = append(r.inputs, append([]byte(nil), input...))
	var req request
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, nil, err
	}
	switch req.Operation {
	case OperationProbe:
		audio := "false"
		if r.hasAudio {
			audio = "true"
		}
		return []byte(fmt.Sprintf(`{"ok":true,"operation":"probe","metadata":{"duration_sec":1.0,"has_video":true,"has_audio":%s}}`, audio)), nil, nil
	case OperationRenderStock, OperationMuxAudioCopy:
		return []byte(fmt.Sprintf(`{"ok":true,"operation":%q}`, req.Operation)), nil, nil
	default:
		return nil, nil, fmt.Errorf("unexpected operation %q", req.Operation)
	}
}

func newStockRendererForPlan(t *testing.T, runner *stockRenderRunner) (*StockRenderer, string, string) {
	t.Helper()
	dir := t.TempDir()
	inputs := []string{filepath.Join(dir, "cut-0.mp4")}
	for _, path := range inputs {
		if err := os.WriteFile(path, []byte("cut clip bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	renderer := &StockRenderer{client: client, policy: mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 23}, profile: mediaexec.VideoProfile{}.WithDefaults()}
	return renderer, inputs[0], filepath.Join(dir, "composed.mp4")
}

// TestStockRenderer_RenderTransportsCanonicalPlan pins the migration: Render
// must send a sealed render_plan (not the removed legacy transitions/effects
// envelope) so the Rust executor no longer fails closed with
// "render_stock requires a canonical render_plan".
func TestStockRenderer_RenderTransportsCanonicalPlan(t *testing.T) {
	runner := &stockRenderRunner{hasAudio: true}
	renderer, inputPath, outputPath := newStockRendererForPlan(t, runner)

	result, err := renderer.Render(context.Background(), stockpipeline.RenderRequest{
		InputPaths: []string{inputPath}, OutputPath: outputPath,
		Codec: "h264_nvenc", Preset: "p1", CRF: 23, Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		NoTransitions: true, NoEffects: true, ClipDurationSec: 5,
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if result.UsedFastPath {
		t.Error("canonical plan render runs a filter_complex graph, so UsedFastPath must be false")
	}
	if len(runner.inputs) != 2 {
		t.Fatalf("expected probe + render_stock, got %d calls", len(runner.inputs))
	}
	var sent request
	if err := json.Unmarshal(runner.inputs[1], &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Operation != OperationRenderStock || sent.OutputPath != outputPath {
		t.Fatalf("unexpected render envelope: %+v", sent)
	}
	if len(sent.RenderPlan) == 0 || string(sent.RenderPlan) == "null" {
		t.Fatal("Render must transport a sealed render_plan")
	}
	if sent.Codec != "h264_nvenc" || sent.Preset != "p1" || sent.CRF != 23 || sent.Width != 1920 || sent.Height != 1080 || sent.FPSNum != 24 || sent.FPSDen != 1 {
		t.Fatalf("encoder policy/profile not transported: %+v", sent)
	}
	var plan render.RenderPlan
	if err := json.Unmarshal(sent.RenderPlan, &plan); err != nil {
		t.Fatalf("decode transported render_plan: %v", err)
	}
	if plan.Version != render.PlanVersion || plan.OutputPath != outputPath {
		t.Fatalf("unexpected plan identity: version=%q output=%q", plan.Version, plan.OutputPath)
	}
	if len(plan.Manifest) != 1 || plan.Manifest[0].Path != inputPath || plan.Manifest[0].FrameCount != 24 {
		t.Fatalf("unexpected plan manifest: %+v", plan.Manifest)
	}
}

// TestStockRenderer_RenderFailsClosedOnTransitionsAndEffects pins the
// godlike/07 contract: a requested transition/effect has no representation in
// the canonical plan, so it must fail loudly rather than be dropped.
func TestStockRenderer_RenderFailsClosedOnTransitionsAndEffects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input stockpipeline.RenderRequest
	}{
		{"transitions", stockpipeline.RenderRequest{InputPaths: []string{"a.mp4", "b.mp4"}, OutputPath: "out.mp4", NoEffects: true, Transitions: []stockpipeline.RenderTransition{{ClipIndex: 1, Segment: "end", ID: "fadeblack"}}}},
		{"effects", stockpipeline.RenderRequest{InputPaths: []string{"a.mp4"}, OutputPath: "out.mp4", NoTransitions: true, EffectPaths: []stockpipeline.RenderEffectPath{{ClipIndex: 0, Path: "/effects/a.mp4"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &stockRenderRunner{}
			renderer, _, _ := newStockRendererForPlan(t, runner)
			if _, err := renderer.Render(context.Background(), tc.input); err == nil {
				t.Fatal("resolved transition/effect must fail closed in the canonical plan path")
			}
			if len(runner.inputs) != 0 {
				t.Fatalf("Rust must not be invoked, got %d calls", len(runner.inputs))
			}
		})
	}
}

// TestStockRenderer_RenderPreservesAudioViaMux pins audio preservation: the
// canonical executor emits video-only, so when the caller keeps audio the
// adapter must mux the original track back with a copy-only operation.
func TestStockRenderer_RenderPreservesAudioViaMux(t *testing.T) {
	runner := &stockRenderRunner{hasAudio: true}
	renderer, inputPath, outputPath := newStockRendererForPlan(t, runner)

	if _, err := renderer.Render(context.Background(), stockpipeline.RenderRequest{
		InputPaths: []string{inputPath}, OutputPath: outputPath, KeepAudio: true,
		NoTransitions: true, NoEffects: true, Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
	}); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(runner.inputs) != 3 {
		t.Fatalf("expected probe + render_stock + mux_audio_copy, got %d calls", len(runner.inputs))
	}
	var renderCall request
	if err := json.Unmarshal(runner.inputs[1], &renderCall); err != nil {
		t.Fatal(err)
	}
	if renderCall.OutputPath != outputPath+".video.mp4" || renderCall.KeepAudio {
		t.Fatalf("render_stock must write the intermediate and strip audio: %+v", renderCall)
	}
	var mux request
	if err := json.Unmarshal(runner.inputs[2], &mux); err != nil {
		t.Fatal(err)
	}
	if mux.Operation != OperationMuxAudioCopy || len(mux.InputPaths) != 2 || mux.InputPaths[1] != inputPath || mux.OutputPath != outputPath {
		t.Fatalf("audio must be restored via copy-only mux: %+v", mux)
	}
}

// TestStockRenderer_RenderSkipsMuxForSilentSource pins that KeepAudio on a
// source with no audio stream does not attempt an impossible copy.
func TestStockRenderer_RenderSkipsMuxForSilentSource(t *testing.T) {
	runner := &stockRenderRunner{hasAudio: false}
	renderer, inputPath, outputPath := newStockRendererForPlan(t, runner)

	if _, err := renderer.Render(context.Background(), stockpipeline.RenderRequest{
		InputPaths: []string{inputPath}, OutputPath: outputPath, KeepAudio: true,
		NoTransitions: true, NoEffects: true, Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
	}); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(runner.inputs) != 2 {
		t.Fatalf("silent source must skip the mux, got %d calls", len(runner.inputs))
	}
}
