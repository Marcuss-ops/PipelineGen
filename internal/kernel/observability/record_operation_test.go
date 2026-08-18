package observability

import (
	"context"
	"testing"
)

func TestRunRecordOperationAppendsWithDuration(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "job-1", AttemptID: "attempt-1"})

	run.RecordOperation(OperationInfo{
		Stage:     StageName("audio_compile"),
		Component: ComponentName("audio"),
		Operation: OperationName("mix"),
	}, 5211)
	run.Finish()

	report := run.Report()
	if len(report.Operations) != 1 {
		t.Fatalf("operations = %d, want 1", len(report.Operations))
	}
	op := report.Operations[0]
	if op.Stage != "audio_compile" || op.Component != "audio" || op.Operation != "mix" {
		t.Errorf("operation identity = %s/%s/%s", op.Stage, op.Component, op.Operation)
	}
	if op.DurationMs != 5211 || op.Status != StageStatusCompleted {
		t.Errorf("operation = duration %d status %s, want 5211/%s", op.DurationMs, op.Status, StageStatusCompleted)
	}
	if op.ObservationID == "" {
		t.Error("operation observation id is empty")
	}
}

func TestRunRecordOperationSkipsEmptyOperation(t *testing.T) {
	run := NewRunObserver(nil).StartRun(context.Background(), RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	run.RecordOperation(OperationInfo{Stage: "audio_compile"}, 100)
	run.Finish()
	if got := len(run.Report().Operations); got != 0 {
		t.Fatalf("operations = %d, want 0 (empty operation name must not record)", got)
	}
}

func TestRunRecordOperationClampsNegativeDuration(t *testing.T) {
	run := NewRunObserver(nil).StartRun(context.Background(), RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	run.RecordOperation(OperationInfo{Component: "audio", Operation: "probe"}, -5)
	run.Finish()
	if got := run.Report().Operations[0].DurationMs; got != 0 {
		t.Fatalf("duration = %d, want 0 (negative clamped)", got)
	}
}

func TestRecordOperationFromContextNoRunIsNoop(t *testing.T) {
	// No run bound: must be a safe no-op, never a panic.
	RecordOperation(context.Background(), OperationInfo{Component: "audio", Operation: "mix"}, 10)
}

func TestRecordOperationFromContextRecordsOnBoundRun(t *testing.T) {
	run := NewRunObserver(nil).StartRun(context.Background(), RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := WithRun(context.Background(), run)

	RecordOperation(ctx, OperationInfo{Component: "audio", Operation: "hash"}, 287)
	run.Finish()

	report := run.Report()
	if len(report.Operations) != 1 || report.Operations[0].Operation != "hash" || report.Operations[0].DurationMs != 287 {
		t.Fatalf("operations = %+v", report.Operations)
	}
}

func TestRecordMeasuredOperationKeepsAnalyticsPayloadCanonical(t *testing.T) {
	run := NewRunObserver(nil).StartRun(context.Background(), RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := WithRun(context.Background(), run)
	RecordMeasuredOperation(ctx, MeasuredOperation{
		Stage: "render", Component: "rust", Provider: "ffmpeg", Operation: "mux",
		ElapsedMS: 42, SourceDurationMS: 1000, SourceSizeBytes: 12, OutputSizeBytes: 34,
		CPUUserMS: 7, CacheHit: true, Strategy: "copy",
	})
	got := run.Report()
	if len(got.Operations) != 1 {
		t.Fatalf("operations = %d, want 1", len(got.Operations))
	}
	op := got.Operations[0]
	if op.Stage != "render" || op.Operation != "mux" || op.DurationMs != 42 || op.OutputSizeBytes != 34 || !op.CacheHit {
		t.Fatalf("canonical operation lost measurement facts: %+v", op)
	}
}
