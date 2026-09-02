package scriptgeneration

import (
	"context"
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func TestRecordAudioOperationsProjectsSubtimings(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)
	r := &Runner{}

	r.recordAudioCompileOperations(ctx, AudioCompileTimings{AudioAssetResolveMS: 12, TimelineCompileMS: 30, ClipAudioPrepareMS: 40, AudioPlanCompileMS: 25})
	r.recordAudioRenderOperations(ctx, AudioPipelineMetrics{MixMS: 5000, AACEncodeMS: 8000, ProbeMS: 390, HashMS: 280})
	r.recordAudioOperation(ctx, "upload", "drive", 3700)
	run.Finish()

	ops := run.Report().Operations
	want := map[string]struct {
		component string
		ms        int64
	}{
		"audio_asset_resolve": {component: "audio", ms: 12},
		"timeline_compile":    {component: "audio", ms: 30},
		"clip_audio_prepare":  {component: "audio", ms: 40},
		"audio_plan_compile":  {component: "audio", ms: 25},
		"mix":                 {component: "audio", ms: 5000},
		"aac_encode":          {component: "audio", ms: 8000},
		"probe":               {component: "audio", ms: 390},
		"hash":                {component: "audio", ms: 280},
		"upload":              {component: "drive", ms: 3700},
	}
	if len(ops) != len(want) {
		t.Fatalf("operations = %d, want %d", len(ops), len(want))
	}
	for _, op := range ops {
		w, ok := want[op.Operation]
		if !ok {
			t.Errorf("unexpected operation %q", op.Operation)
			continue
		}
		if op.Stage != audioCompileStage || op.Component != w.component || op.DurationMs != w.ms {
			t.Errorf("operation %q = stage=%s component=%s ms=%d, want %s/%s/%d",
				op.Operation, op.Stage, op.Component, op.DurationMs, audioCompileStage, w.component, w.ms)
		}
	}
}

func TestRecordAudioOperationsSkipUnmeasured(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)
	r := &Runner{}

	// All-zero metrics: nothing must be recorded (no fake zero-length ops).
	r.recordAudioCompileOperations(ctx, AudioCompileTimings{})
	r.recordAudioRenderOperations(ctx, AudioPipelineMetrics{})
	r.recordAudioOperation(ctx, "upload", "drive", 0)
	run.Finish()

	if got := len(run.Report().Operations); got != 0 {
		t.Fatalf("operations = %d, want 0", got)
	}
}

func TestRecordAudioOperationNoRunIsNoop(t *testing.T) {
	r := &Runner{}
	// No run bound to ctx: instrumentation must be a safe no-op, never a panic.
	r.recordAudioOperation(context.Background(), "mix", "audio", 100)
}
