// Package stockpipeline — step_sub_operations_test.go
// (PR-STOCK-SUB-OPERATIONS, October 2026).
//
// Contract tests for the stock-job sub-operation decomposition and the
// stock job steps (stock.stage_sources / stock.extract / stock.compose) were
// ONE wall each: a multi-source request measured as a single span, and the
// operator could not tell download time from cut time from render time. These
// tests pin:
//
//  1. stock.stage_sources records ONE canonical download operation per unique
//     source (deduplicated: two plans sharing a source measure once);
//  2. stock.extract records ONE cut operation per source group, carrying the
//     batch cardinality;
//  3. stock.compose records ONE render operation per composed chunk;
//     "Prepare failed" line — and the typed sentinel stays classifiable even
//
// The sub-operations are canonical kernel operations (run_operation_observations):
// the enclosing stage wall is untouched, the operations are its measured
// sub-spans.
package stockpipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// findOp returns the first recorded operation with the given stage + operation
// name, or nil. Concurrent sub-operations complete in arbitrary order, so
// assertions select by name instead of index.
func findOp(ops []kernobs.OperationReport, stage, operation string) *kernobs.OperationReport {
	for i := range ops {
		if ops[i].Stage == stage && ops[i].Operation == operation {
			return &ops[i]
		}
	}
	return nil
}

func countOps(ops []kernobs.OperationReport, stage, operation string) int {
	count := 0
	for _, op := range ops {
		if op.Stage == stage && op.Operation == operation {
			count++
		}
	}
	return count
}

// TestStockStageSources_RecordsOneDownloadOperationPerUniqueSource pins the
// stage_sources decomposition: three plans over two unique sources measure TWO
// download operations (SourceID dedupe preserved), attributed per provider.
func TestStockStageSources_RecordsOneDownloadOperationPerUniqueSource(t *testing.T) {
	ctx, recorder := newTimingObservedRun(t)
	plans := []ClipPlan{
		{SourceID: "https://www.youtube.com/watch?v=aaa", SourceProvider: SourceProviderYouTube, StartSec: 0, EndSec: 5, PolicyVersion: "test-policy-v1"},
		// Duplicate source (same video, later clip): must NOT pay a second
		// measured download — the step deduplicates before staging.
		{SourceID: "https://www.youtube.com/watch?v=aaa", SourceProvider: SourceProviderYouTube, StartSec: 5, EndSec: 10, PolicyVersion: "test-policy-v1"},
		{SourceID: "https://example.com/b.mp4", SourceProvider: SourceProviderUnknown, StartSec: 0, EndSec: 5, PolicyVersion: "test-policy-v1"},
	}
	stager := &boundedStagingStager{}
	runner := newStagingFakeRunner(plans, stager, 2)

	if err := (StockStageSourcesStep{}).Run(ctx, runner); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := countOps(recorder.operations, string(kernobs.StageStockStageSources), string(kernobs.OperationDownload)); got != 2 {
		t.Fatalf("download operations = %d, want 2 (one per UNIQUE source); got %+v", got, recorder.operations)
	}
	yt := findOp(recorder.operations, string(kernobs.StageStockStageSources), string(kernobs.OperationDownload))
	if yt == nil {
		t.Fatal("no download operation recorded")
	}
	if yt.Items != 1 {
		t.Fatalf("download operation Items = %d, want 1 (one source is one download)", yt.Items)
	}
	if yt.Status != kernobs.StageStatusCompleted {
		t.Fatalf("download operation status = %q, want completed", yt.Status)
	}
	// Provider attribution: the youtube plan must land on the youtube
	// component; the unknown bucket on the stager boundary.
	var sawYouTube, sawStager bool
	for _, op := range recorder.operations {
		if op.Component == string(kernobs.ComponentYouTube) && op.Provider == SourceProviderYouTube {
			sawYouTube = true
		}
		if op.Component == "source_stager" && op.Provider == SourceProviderUnknown {
			sawStager = true
		}
	}
	if !sawYouTube || !sawStager {
		t.Fatalf("provider attribution missing (youtube=%v stager=%v); ops: %+v", sawYouTube, sawStager, recorder.operations)
	}
}

// subOpCutter is a trivial VideoCutter that succeeds every job.
type subOpCutter struct{ calls int }

func (c *subOpCutter) Cut(_ context.Context, req CutRequest) (CutBatchResult, error) {
	c.calls++
	items := make([]CutItemResult, len(req.Jobs))
	for i := range req.Jobs {
		items[i] = CutItemResult{JobID: req.Jobs[i].OutputPath, OutputPath: req.Jobs[i].OutputPath, Status: CutItemStatusSucceeded}
	}
	return CutBatchResult{SourcePath: req.SourcePath, Items: items}, nil
}

// TestStockExtract_RecordsOneCutOperationPerSourceGroup pins the extract
// decomposition: one source group is ONE measured cut batch carrying its
// cardinality, so the extract wall splits per source.
func TestStockExtract_RecordsOneCutOperationPerSourceGroup(t *testing.T) {
	ctx, recorder := newTimingObservedRun(t)
	cutter := &subOpCutter{}
	runner := &fakeStepRunner{
		runInput: &RunInput{ClipDuration: 5, TotalMinutes: 1},
		cfg:      OrchestratorConfig{PolicyVersion: "test-policy-v1"},
		state:    &RunState{},
		cutter:   cutter,
	}
	groupPlans := []ClipPlan{
		{StartSec: 0, EndSec: 5, OutputLogicalID: "clip-0"},
		{StartSec: 5, EndSec: 10, OutputLogicalID: "clip-1"},
	}

	result, err := executeCuts(ctx, runner, "src-1", "/staged/src-1.mp4", 30, groupPlans, 0, false, 0)
	if err != nil {
		t.Fatalf("executeCuts: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("cut items = %d, want 2", len(result.Items))
	}

	op := findOp(recorder.operations, string(kernobs.StageStockExtract), string(kernobs.OperationCut))
	if op == nil {
		t.Fatalf("no cut operation recorded; ops: %+v", recorder.operations)
	}
	if op.Component != string(kernobs.ComponentFFmpeg) {
		t.Fatalf("cut operation component = %q, want %q", op.Component, kernobs.ComponentFFmpeg)
	}
	if op.Items != 2 {
		t.Fatalf("cut operation Items = %d, want the batch cardinality 2", op.Items)
	}
	if op.Status != kernobs.StageStatusCompleted {
		t.Fatalf("cut operation status = %q, want completed", op.Status)
	}
}

// subOpRenderer is a trivial StockRenderer that counts render passes.
type subOpRenderer struct{ calls int }

func (r *subOpRenderer) Render(_ context.Context, _ RenderRequest) (RenderResult, error) {
	r.calls++
	return RenderResult{}, nil
}

// renderingFakeRunner overrides the base fake's nil Renderer.
type renderingFakeRunner struct {
	*fakeStepRunner
	renderer StockRenderer
}

func (r *renderingFakeRunner) Renderer() StockRenderer { return r.renderer }

// TestStockCompose_RecordsOneRenderOperationPerChunk pins the compose
// decomposition: one chunk is ONE measured render pass (1:1 cardinality).
func TestStockCompose_RecordsOneRenderOperationPerChunk(t *testing.T) {
	ctx, recorder := newTimingObservedRun(t)
	renderer := &subOpRenderer{}
	runner := &renderingFakeRunner{
		fakeStepRunner: &fakeStepRunner{
			runInput: &RunInput{ClipDuration: 5, TotalMinutes: 1},
			cfg:      OrchestratorConfig{PolicyVersion: "test-policy-v1"},
			state:    &RunState{CutPaths: []string{"/tmp/cut_0.mp4", "/tmp/cut_1.mp4"}},
		},
		renderer: renderer,
	}

	if err := (StockComposeChunksStep{}).Run(ctx, runner); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if renderer.calls != 2 {
		t.Fatalf("render calls = %d, want 2 (one per chunk)", renderer.calls)
	}
	if got := countOps(recorder.operations, string(kernobs.StageStockCompose), string(kernobs.OperationRender)); got != 2 {
		t.Fatalf("render operations = %d, want 2 (one per chunk); ops: %+v", got, recorder.operations)
	}
	op := findOp(recorder.operations, string(kernobs.StageStockCompose), string(kernobs.OperationRender))
	if op.Component != string(kernobs.ComponentFFmpeg) || op.Items != 1 {
		t.Fatalf("render operation = component %q items %d, want ffmpeg/1", op.Component, op.Items)
	}
}
