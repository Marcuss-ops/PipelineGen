package stockpipeline

import (
	"context"
	"os"
	"strings"
	"testing"
)

type p0CountingRenderer struct {
	calls int
}

func (r *p0CountingRenderer) Render(_ context.Context, _ RenderRequest) (RenderResult, error) {
	r.calls++
	return RenderResult{}, nil
}

type p0ComposeRunner struct {
	*fakeStepRunner
	renderer StockRenderer
}

func (r *p0ComposeRunner) Renderer() StockRenderer { return r.renderer }

func TestStockComposeChunks_BypassesRendererForCanonicalCutterOutput(t *testing.T) {
	cutPaths := []string{"/data/stock/workspaces/job/extracted/stock_final_job_0_0.mp4", "/data/stock/workspaces/job/extracted/stock_final_job_0_1.mp4"}
	renderer := &p0CountingRenderer{}
	runner := &p0ComposeRunner{
		fakeStepRunner: &fakeStepRunner{
			runInput: &RunInput{NoEffects: true, NoTransitions: true},
			cfg:      OrchestratorConfig{JobId: "job", ClipDurationSec: 5},
			state:    &RunState{CutPaths: cutPaths},
		},
		renderer: renderer,
	}

	if err := (StockComposeChunksStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockComposeChunksStep.Run returned unexpected error: %v", err)
	}
	if renderer.calls != 0 {
		t.Fatalf("renderer calls = %d, want 0 when cutter output is canonical", renderer.calls)
	}
	if got := runner.State().ComposedPaths; len(got) != len(cutPaths) {
		t.Fatalf("ComposedPaths length = %d, want %d", len(got), len(cutPaths))
	}
	for i, got := range runner.State().ComposedPaths {
		if got != cutPaths[i] {
			t.Errorf("ComposedPaths[%d] = %q, want cutter path %q", i, got, cutPaths[i])
		}
	}
}

func TestStockComposeChunks_RendersWhenEffectsOrTransitionsAreRequired(t *testing.T) {
	cutPaths := []string{"/data/stock/workspaces/job/extracted/stock_cut_job_0_0.mp4", "/data/stock/workspaces/job/extracted/stock_cut_job_0_1.mp4"}
	renderer := &p0CountingRenderer{}
	runner := &p0ComposeRunner{
		fakeStepRunner: &fakeStepRunner{
			runInput: &RunInput{NoEffects: false, NoTransitions: true},
			cfg:      OrchestratorConfig{JobId: "job", ClipDurationSec: 5},
			state:    &RunState{CutPaths: cutPaths},
		},
		renderer: renderer,
	}

	if err := (StockComposeChunksStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockComposeChunksStep.Run returned unexpected error: %v", err)
	}
	if renderer.calls != len(cutPaths) {
		t.Fatalf("renderer calls = %d, want %d when effects are required", renderer.calls, len(cutPaths))
	}
	if got := runner.State().ComposedPaths; len(got) != len(cutPaths) {
		t.Fatalf("ComposedPaths length = %d, want %d", len(got), len(cutPaths))
	}
	for _, got := range runner.State().ComposedPaths {
		if !strings.Contains(got, "stock_composed_") {
			t.Errorf("composed path = %q, want stock_composed output", got)
		}
	}

	// The other non-canonical combination must also retain composition.
	runner.fakeStepRunner.runInput = &RunInput{NoEffects: true, NoTransitions: false}
	runner.fakeStepRunner.state.ComposedPaths = nil
	renderer.calls = 0
	if err := (StockComposeChunksStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockComposeChunksStep.Run with transitions returned unexpected error: %v", err)
	}
	if renderer.calls != len(cutPaths) {
		t.Fatalf("renderer calls with transitions = %d, want %d", renderer.calls, len(cutPaths))
	}
}

func TestStockComposeChunks_DispatchBypassRequiresBothFlags(t *testing.T) {
	if !shouldBypassStockCompose(StepKeyStockComposeChunks, &RunInput{NoEffects: true, NoTransitions: true}) {
		t.Fatal("compose dispatch should be bypassed for canonical cutter output")
	}
	if shouldBypassStockCompose(StepKeyStockComposeChunks, &RunInput{NoEffects: true, NoTransitions: false}) {
		t.Fatal("compose dispatch must remain enabled when transitions are requested")
	}
	if shouldBypassStockCompose(StepKeyStockComposeChunks, &RunInput{NoEffects: false, NoTransitions: true}) {
		t.Fatal("compose dispatch must remain enabled when effects are requested")
	}
	if shouldBypassStockCompose(StepKeyStockComposeChunks, nil) {
		t.Fatal("nil input must retain the conservative compose path")
	}
	if shouldBypassStockCompose(StepKeyStockPublish, &RunInput{NoEffects: true, NoTransitions: true}) {
		t.Fatal("only stock.compose_chunks may be bypassed")
	}
}

func TestStockComposeChunks_CanonicalStateIsReadyForPublish(t *testing.T) {
	cutPaths := []string{"/data/stock/workspaces/job/extracted/stock_final_job_0_0.mp4"}
	runner := &p0ComposeRunner{
		fakeStepRunner: &fakeStepRunner{
			runInput: &RunInput{NoEffects: true, NoTransitions: true},
			cfg:      OrchestratorConfig{JobId: "job", ClipDurationSec: 5},
			state:    &RunState{CutPaths: cutPaths},
		},
		renderer: &p0CountingRenderer{},
	}
	if isCanonicalFinalCut(runner.RunInput()) != true {
		t.Fatal("canonical input was not recognized")
	}
	// This mirrors the state hand-off made by extract_clips when the
	// orchestrator omits compose_chunks.
	runner.State().ComposedPaths = append([]string(nil), runner.State().CutPaths...)
	if len(runner.State().ComposedPaths) != 1 || runner.State().ComposedPaths[0] != cutPaths[0] {
		t.Fatalf("ComposedPaths = %v, want canonical cutter paths", runner.State().ComposedPaths)
	}
}

func TestExecuteCuts_UsesFinalArtifactNameForCanonicalCut(t *testing.T) {
	runner := newFakeRunner(nil, 5, "test-policy-v1")
	runner.runInput.NoEffects = true
	runner.runInput.NoTransitions = true
	runner.cutter = &p0RecordingCutter{}

	_, err := executeCuts(
		context.Background(), runner,
		"source-a",
		"/tmp/source-a.mp4",
		60,
		[]ClipPlan{{SourceID: "source-a", StartSec: 0, EndSec: 5}},
		0,
		false,
	)
	if err != nil {
		t.Fatalf("executeCuts returned unexpected error: %v", err)
	}

	request := runner.cutter.(*p0RecordingCutter).request
	if len(request.Jobs) != 1 {
		t.Fatalf("cut jobs = %d, want 1", len(request.Jobs))
	}
	if !strings.Contains(request.Jobs[0].OutputPath, "/stock_final_") {
		t.Fatalf("canonical cut output = %q, want stock_final naming", request.Jobs[0].OutputPath)
	}
}

type p0RecordingCutter struct {
	request CutRequest
}

func (c *p0RecordingCutter) Cut(_ context.Context, req CutRequest) (CutBatchResult, error) {
	c.request = req
	items := make([]CutItemResult, len(req.Jobs))
	for i, job := range req.Jobs {
		if err := os.WriteFile(job.OutputPath, []byte("canonical-cut"), 0o644); err != nil {
			return CutBatchResult{}, err
		}
		items[i] = CutItemResult{
			JobID:      job.OutputPath,
			OutputPath: job.OutputPath,
			Status:     CutItemStatusSucceeded,
			SizeBytes:  1,
		}
	}
	return CutBatchResult{SourcePath: req.SourcePath, Items: items}, nil
}
