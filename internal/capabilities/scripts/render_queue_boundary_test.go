package scriptgeneration

import (
	"context"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// TestRecordQueueBoundaryPhasesMapsSubmitAndWaitCompletion pins the boundary
// projection: the two halves the CALLER owns (submit round-trip, terminal-state
// wait) become owner-measured operations under StageOverlayRender so a slow
// overlay stage wall decomposes as submit + wait + Σ worker-reported phases.
// The component is render_queue — the boundary belongs to the queue client, not
// to RenderingGen's worker phases.
func TestRecordQueueBoundaryPhasesMapsSubmitAndWaitCompletion(t *testing.T) {
	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: "job-boundary", JobType: "overlay.render", AttemptID: "attempt-boundary"})
	ctx := kernobs.WithRun(context.Background(), run)

	recordQueueBoundaryPhases(ctx, 12, 300_000)

	report := run.Finish()
	if len(report.Operations) != 2 {
		t.Fatalf("operations=%d (%+v), want exactly submit + wait_completion", len(report.Operations), report.Operations)
	}
	want := map[string]int64{"submit": 12, "wait_completion": 300_000}
	for _, op := range report.Operations {
		ms, ok := want[op.Operation]
		if !ok {
			t.Fatalf("unexpected operation %q", op.Operation)
		}
		if op.Component != string(kernobs.ComponentRenderQueue) {
			t.Fatalf("operation %s component=%q, want render_queue", op.Operation, op.Component)
		}
		if op.Stage != string(StageOverlayRender) {
			t.Fatalf("operation %s stage=%q, want overlay_render", op.Operation, op.Stage)
		}
		if op.DurationMs != ms {
			t.Fatalf("operation %s duration=%d, want %d", op.Operation, op.DurationMs, ms)
		}
		if op.Status != kernobs.StageStatusCompleted {
			t.Fatalf("operation %s status=%q, want completed", op.Operation, op.Status)
		}
	}
}

// TestRecordQueueBoundaryPhasesSkipsUnmeasuredHalves pins the no-fake-zero
// rule shared with the worker-phase projection: a half the caller could not
// measure (submit failed before accept; zero wait) records nothing.
func TestRecordQueueBoundaryPhasesSkipsUnmeasuredHalves(t *testing.T) {
	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: "job-boundary", AttemptID: "attempt-boundary"})
	ctx := kernobs.WithRun(context.Background(), run)

	recordQueueBoundaryPhases(ctx, 0, 0)
	recordQueueBoundaryPhases(ctx, -5, 0)
	recordQueueBoundaryPhases(ctx, 7, -1)

	report := run.Finish()
	if len(report.Operations) != 1 || report.Operations[0].Operation != "submit" || report.Operations[0].DurationMs != 7 {
		t.Fatalf("only the positive submit must survive: %+v", report.Operations)
	}
}

// slowBoundaryRenderClient makes both boundary halves measurable at the
// canonical int64-ms storage granularity: the submit round-trip sleeps, and
// the event-driven wait parks for a poll interval before the terminal state.
// A boundary faster than 1ms floors to 0ms and is — by the same no-fake-zero
// rule the worker phases obey — not recorded.
type slowBoundaryRenderClient struct {
	fakeRenderQueueClient
}

func (c *slowBoundaryRenderClient) Submit(ctx context.Context, job RenderQueueJob) error {
	time.Sleep(3 * time.Millisecond)
	return c.fakeRenderQueueClient.Submit(ctx, job)
}

func (c *slowBoundaryRenderClient) WaitTerminal(ctx context.Context, id string) (RenderQueueJob, error) {
	select {
	case <-ctx.Done():
		return RenderQueueJob{}, ctx.Err()
	case <-time.After(5 * time.Millisecond):
	}
	return c.fakeRenderQueueClient.Get(ctx, id)
}

// TestEnqueueChrononPlanRecordsBoundaryOperations is the end-to-end pin on the
// live enqueue path: a plan through EnqueueChrononPlan produces submit AND
// wait_completion operations beside the worker phases, all bound to the run
// in ctx and measured by the caller's own clock — never re-derived.
func TestEnqueueChrononPlanRecordsBoundaryOperations(t *testing.T) {
	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: "job-e2e", JobType: "overlay.render", AttemptID: "attempt-e2e"})
	ctx := kernobs.WithRun(context.Background(), run)

	plan := capoverlay.GoldenOverlayPlanV1()
	client := &slowBoundaryRenderClient{fakeRenderQueueClient: fakeRenderQueueClient{jobs: map[string]RenderQueueJob{plan.PlanID: {ID: plan.PlanID, State: RenderQueueStateCompleted}}}}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.SetPollInterval(time.Millisecond)

	ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Status != "COMPLETED" {
		t.Fatalf("status=%q, want COMPLETED", ref.Status)
	}

	report := run.Finish()
	var submit, wait *kernobs.OperationReport
	for i := range report.Operations {
		switch report.Operations[i].Operation {
		case "submit":
			submit = &report.Operations[i]
		case "wait_completion":
			wait = &report.Operations[i]
		}
	}
	if submit == nil || wait == nil {
		t.Fatalf("boundary operations missing from %+v", report.Operations)
	}
	if submit.Component != string(kernobs.ComponentRenderQueue) || wait.Component != string(kernobs.ComponentRenderQueue) {
		t.Fatalf("boundary component must be render_queue: submit=%q wait=%q", submit.Component, wait.Component)
	}
	if submit.Stage != string(StageOverlayRender) || wait.Stage != string(StageOverlayRender) {
		t.Fatalf("boundary stage must be overlay_render: submit=%q wait=%q", submit.Stage, wait.Stage)
	}
	if submit.DurationMs <= 0 || wait.DurationMs <= 0 {
		t.Fatalf("boundary durations must be positive: submit=%d wait=%d", submit.DurationMs, wait.DurationMs)
	}
}
