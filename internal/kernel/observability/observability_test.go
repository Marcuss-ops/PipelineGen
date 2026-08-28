package observability

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// ── test doubles ─────────────────────────────────────────────────────

// fakeClock advances 1ms per read so duration assertions are deterministic.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(0, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Millisecond)
	return c.t
}

type captureRecorder struct {
	mu      sync.Mutex
	reports []*RunReport
}

// ctxCapturingRecorder records the context error observed by SaveReport so
// a test can prove the detached emit flush is NOT cancelled even when the
// run's parent context was cancelled before Finish.
type ctxCapturingRecorder struct {
	mu      sync.Mutex
	reports []*RunReport
	ctxErr  error
}

func (c *ctxCapturingRecorder) SaveReport(ctx context.Context, rep *RunReport) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = append(c.reports, rep)
	c.ctxErr = ctx.Err()
	return nil
}

func (c *ctxCapturingRecorder) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reports)
}

func (c *ctxCapturingRecorder) saveCtxErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ctxErr
}

type captureCollector struct {
	mu      sync.Mutex
	reports []*RunReport
	mutate  bool
}

func (c *captureCollector) Collect(_ context.Context, rep *RunReport) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mutate {
		rep.Status = "MUTATED"
	}
	c.reports = append(c.reports, rep)
	return nil
}

func (c *captureCollector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reports)
}

func (c *captureRecorder) SaveReport(_ context.Context, rep *RunReport) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = append(c.reports, rep)
	return nil
}

func (c *captureRecorder) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reports)
}

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("expected panic, got none")
		}
	}()
	fn()
}

// ── run lifecycle ────────────────────────────────────────────────────

func TestRun_FinishSucceedsWithCanonicalEnvelope(t *testing.T) {
	obs := NewRunObserver(nil)
	obs.now = newFakeClock().Now
	run := obs.StartRun(context.Background(), RunInfo{
		RunID: "run-1", JobID: "job-1", JobType: "script.generate",
		AttemptID: "attempt-1", QueueWaitMs: 450,
	})

	rep := run.Finish()

	if rep.Status != StatusSucceeded {
		t.Fatalf("status = %q, want %q", rep.Status, StatusSucceeded)
	}
	if rep.RunID != "run-1" || rep.JobID != "job-1" || rep.JobType != "script.generate" {
		t.Fatalf("identity = %q/%q/%q", rep.RunID, rep.JobID, rep.JobType)
	}
	if rep.QueueWaitMs != 450 {
		t.Fatalf("queue_wait_ms = %d, want 450", rep.QueueWaitMs)
	}
	if rep.WallTimeMs <= 0 {
		t.Fatalf("wall_time_ms = %d, want > 0", rep.WallTimeMs)
	}
	if rep.FinishedAt.IsZero() {
		t.Fatal("finished_at must be set")
	}
	if rep.CreatedAt.IsZero() || rep.StartedAt.IsZero() {
		t.Fatal("created_at/started_at must be set")
	}
}

func TestRun_StartRunGeneratesRunIDWhenEmpty(t *testing.T) {
	obs := NewRunObserver(nil)
	rep := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"}).Finish()
	if rep.RunID == "" {
		t.Fatal("run_id must be generated when empty")
	}
}

func TestRun_FinishWithErrorMarksFailed(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	rep := run.FinishWithError(errors.New("qdrant offline"))
	if rep.Status != StatusFailed {
		t.Fatalf("status = %q, want %q", rep.Status, StatusFailed)
	}
	if rep.ErrorCode != "error" || rep.Error == "" {
		t.Fatalf("error_code/error = %q/%q", rep.ErrorCode, rep.Error)
	}
}

// TestRun_FinishDetachesSaveReportFromParentCancel is the shutdown-bug
// regression guard: when the worker lifecycle ctx is cancelled before the
// run finishes, the final SaveReport must still receive a live (non-cancelled)
// context — otherwise the terminal UPDATE run_observability never lands and
// the run stays RUNNING forever.
func TestRun_FinishDetachesSaveReportFromParentCancel(t *testing.T) {
	rec := &ctxCapturingRecorder{}
	obs := NewRunObserver(rec)

	ctx, cancel := context.WithCancel(context.Background())
	run := obs.StartRun(ctx, RunInfo{RunID: "run-detach", JobID: "job-detach", JobType: "script.generate", AttemptID: "attempt-detach"})

	cancel() // worker shutdown: parent ctx cancelled

	rep := run.Finish()
	if rep.Status != StatusSucceeded {
		t.Fatalf("status = %q, want SUCCEEDED", rep.Status)
	}
	if rec.count() != 1 {
		t.Fatalf("SaveReport calls = %d, want 1", rec.count())
	}
	if err := rec.saveCtxErr(); err != nil {
		t.Fatalf("SaveReport received a cancelled/expired context (%v) — the run would stay RUNNING", err)
	}
}

type codedError struct{ code string }

func (e codedError) Error() string     { return "typed: " + e.code }
func (e codedError) ErrorCode() string { return e.code }

func TestRun_FinishWithErrorPersistsTypedCode(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	rep := run.FinishWithError(codedError{code: "ffmpeg_probe_failed"})
	if rep.ErrorCode != "ffmpeg_probe_failed" {
		t.Fatalf("error_code = %q, want ffmpeg_probe_failed", rep.ErrorCode)
	}
}

func TestRun_FinishIsIdempotent(t *testing.T) {
	obs := NewRunObserver(nil)
	obs.now = newFakeClock().Now
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	first := run.Finish()
	second := run.FinishWithError(errors.New("late"))
	third := run.Cancel()

	if second.Status != StatusSucceeded || third.Status != StatusSucceeded {
		t.Fatalf("later finishes must not override the first: %q / %q", second.Status, third.Status)
	}
	if !first.FinishedAt.Equal(second.FinishedAt) {
		t.Fatalf("finished_at changed on second finish: %v vs %v", first.FinishedAt, second.FinishedAt)
	}
}

func TestRun_ReportBeforeFinishIsRunning(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	if rep := run.Report(); rep.Status != StatusRunning {
		t.Fatalf("pre-finish status = %q, want RUNNING", rep.Status)
	}
}

func TestRun_NilReceiverSafety(t *testing.T) {
	var run *Run
	if run.Finish() != nil || run.Report() != nil {
		t.Fatal("nil run must return nil report surfaces")
	}
	if raw, err := run.JSON(); raw != nil || err != nil {
		t.Fatalf("nil run JSON must return nil: %v", err)
	}
	called := false
	if err := run.Stage(context.Background(), StageAcquire, func(context.Context) error {
		called = true
		return errors.New("passthrough")
	}); err == nil || !called {
		t.Fatal("nil run Stage must pass through to fn")
	}
}

// ── stages ───────────────────────────────────────────────────────────

func TestRun_StageMeasuresAndCompletes(t *testing.T) {
	obs := NewRunObserver(nil)
	obs.now = newFakeClock().Now
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j", CreatedAt: time.Unix(0, 0)})

	if err := run.Stage(context.Background(), StageAcquire, func(context.Context) error {
		return nil
	}); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	rep := run.Finish()
	if len(rep.Stages) != 1 {
		t.Fatalf("stages = %d, want 1", len(rep.Stages))
	}
	st := rep.Stages[0]
	if st.Name != string(StageAcquire) || st.Status != StageStatusCompleted {
		t.Fatalf("stage = %q/%q", st.Name, st.Status)
	}
	if st.DurationMs <= 0 {
		t.Fatalf("duration_ms = %d, want > 0", st.DurationMs)
	}
	raw, err := run.JSON()
	if err != nil || string(raw) == "" {
		t.Fatalf("run JSON after stage: %v", err)
	}
}

func TestRun_StageFailureRecordsErrorCode(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	err := run.Stage(context.Background(), StageAcquire, func(context.Context) error {
		return codedError{code: "download_failed"}
	})
	if err == nil {
		t.Fatal("Stage must propagate the error")
	}
	st := run.Report().Stages[0]
	if st.Status != StageStatusFailed || st.ErrorCode != "download_failed" {
		t.Fatalf("stage = %q/%q", st.Status, st.ErrorCode)
	}
}

func TestRun_StageWithCounters(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	err := run.StageWith(context.Background(), StageInfo{
		Stage:          StageProcess,
		CacheStatus:    "hit",
		ItemsInput:     20,
		ItemsCompleted: 18,
		ItemsFailed:    2,
		BytesProcessed: 1024,
	}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("StageWith: %v", err)
	}
	st := run.Report().Stages[0]
	if st.ItemsInput != 20 || st.ItemsCompleted != 18 || st.ItemsFailed != 2 || st.BytesProcessed != 1024 {
		t.Fatalf("counters = %d/%d/%d/%d", st.ItemsInput, st.ItemsCompleted, st.ItemsFailed, st.BytesProcessed)
	}
	if st.CacheStatus != "hit" {
		t.Fatalf("cache_status = %q, want hit", st.CacheStatus)
	}
}

func TestRun_StagePanicClosesTimerAndRepanics(t *testing.T) {
	obs := NewRunObserver(nil)
	obs.now = newFakeClock().Now
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})

	mustPanic(t, func() {
		_ = run.Stage(context.Background(), StageAcquire, func(context.Context) error {
			panic("boom")
		})
	})

	rep := run.Report()
	if len(rep.Stages) != 1 {
		t.Fatalf("stages = %d, want 1 (stage timer must be closed on panic)", len(rep.Stages))
	}
	st := rep.Stages[0]
	if st.Status != StageStatusFailed {
		t.Fatalf("stage status = %q, want failed", st.Status)
	}
	if st.DurationMs <= 0 {
		t.Fatalf("stage duration = %d, want > 0", st.DurationMs)
	}
}

func TestRun_UnlabelledStageIsNotMeasured(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	if err := run.Stage(context.Background(), "", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if len(run.Report().Stages) != 0 {
		t.Fatal("empty stage name must not be recorded")
	}
}

// ── operations ───────────────────────────────────────────────────────

func TestRun_OperationMeasuresAndAccumulates(t *testing.T) {
	obs := NewRunObserver(nil)
	obs.now = newFakeClock().Now
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})

	err := run.Operation(context.Background(), OperationInfo{
		Stage:     StageAcquire,
		Component: ComponentYouTube,
		Operation: OperationDownload,
		Provider:  "youtube",
		Bytes:     1000,
	}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("Operation: %v", err)
	}

	rep := run.Finish()
	if len(rep.Operations) != 1 {
		t.Fatalf("operations = %d, want 1", len(rep.Operations))
	}
	op := rep.Operations[0]
	if op.Stage != string(StageAcquire) || op.Component != string(ComponentYouTube) ||
		op.Operation != string(OperationDownload) || op.Provider != "youtube" {
		t.Fatalf("operation = %q/%q/%q/%q", op.Stage, op.Component, op.Operation, op.Provider)
	}
	if op.Status != StageStatusCompleted || op.DurationMs <= 0 {
		t.Fatalf("operation = %q/%d", op.Status, op.DurationMs)
	}
	if op.Bytes != 1000 {
		t.Fatalf("bytes = %d, want 1000", op.Bytes)
	}
	if rep.AccumulatedOperationMs != op.DurationMs {
		t.Fatalf("accumulated_operation_ms = %d, want %d", rep.AccumulatedOperationMs, op.DurationMs)
	}
}

// TestRun_OperationOnRecordAttachesPostCallFacts pins the OnRecord hook:
// an owner that only knows its facts AFTER the call (tokens, model load)
// can attach them to the finalized report before it is recorded, without
// re-timing the boundary.
func TestRun_OperationOnRecordAttachesPostCallFacts(t *testing.T) {
	obs := NewRunObserver(nil)
	obs.now = newFakeClock().Now
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})

	attached := false
	err := run.Operation(context.Background(), OperationInfo{
		Stage:     StageGenerate,
		Component: ComponentOllama,
		Operation: OperationGenerate,
		OnRecord: func(op *OperationReport) {
			attached = true
			op.MetadataJSON = `{"input_tokens":120,"output_tokens":340,"cold_start":true}`
		},
	}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("Operation: %v", err)
	}

	rep := run.Finish()
	if len(rep.Operations) != 1 {
		t.Fatalf("operations = %d, want 1", len(rep.Operations))
	}
	op := rep.Operations[0]
	if !attached {
		t.Fatal("OnRecord was not invoked")
	}
	if op.MetadataJSON != `{"input_tokens":120,"output_tokens":340,"cold_start":true}` {
		t.Fatalf("metadata_json = %q, want the post-call facts", op.MetadataJSON)
	}
	if op.Status != StageStatusCompleted || op.DurationMs <= 0 {
		t.Fatalf("operation = %q/%d, want completed with a duration", op.Status, op.DurationMs)
	}
}

func TestRun_OperationPanicClosesTimerAndRepanics(t *testing.T) {
	obs := NewRunObserver(nil)
	obs.now = newFakeClock().Now
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})

	mustPanic(t, func() {
		_ = run.Operation(context.Background(), OperationInfo{
			Stage: StageAcquire, Component: ComponentDrive, Operation: OperationUpload,
		}, func(context.Context) error { panic("upload boom") })
	})

	rep := run.Report()
	if len(rep.Operations) != 1 || rep.Operations[0].Status != StageStatusFailed {
		t.Fatalf("operation must be closed as failed on panic: %#v", rep.Operations)
	}
	if rep.Operations[0].DurationMs <= 0 {
		t.Fatal("panicked operation must have a closed duration")
	}
}

func TestMeasureOperation_FromContext(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	ctx := WithRun(context.Background(), run)

	called := false
	err := MeasureOperation(ctx, OperationInfo{
		Stage: StagePublish, Component: ComponentDrive, Operation: OperationUpload,
	}, func(context.Context) error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Fatalf("MeasureOperation: called=%v err=%v", called, err)
	}
	if len(run.Report().Operations) != 1 {
		t.Fatalf("operations = %d, want 1", len(run.Report().Operations))
	}
}

func TestMeasureOperation_NoRunIsPassThrough(t *testing.T) {
	called := false
	err := MeasureOperation(context.Background(), OperationInfo{
		Operation: OperationSearch,
	}, func(context.Context) error {
		called = true
		return errors.New("outer")
	})
	if !called || err == nil || err.Error() != "outer" {
		t.Fatalf("pass-through violated: called=%v err=%v", called, err)
	}
}

func TestMeasureStage_FromContext(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	ctx := WithRun(context.Background(), run)

	if err := MeasureStage(ctx, StageVerify, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("MeasureStage: %v", err)
	}
	if len(run.Report().Stages) != 1 {
		t.Fatalf("stages = %d, want 1", len(run.Report().Stages))
	}
}

// ── body runner ──────────────────────────────────────────────────────

func TestRunObserver_StartRunForClaim(t *testing.T) {
	obs := NewRunObserver(nil)
	createdAt := time.Unix(1700000000, 0)
	startedAt := time.Unix(1700000005, 0)
	run := obs.StartRunForClaim(context.Background(), ClaimRunInfo{
		JobID:      "j",
		JobType:    "stock.run",
		AttemptID:  "lease-1",
		CreatedAt:  createdAt,
		StartedAt:  &startedAt,
		RetryCount: 2,
	})

	rep := run.Finish()
	if rep.QueueWaitMs != 5000 {
		t.Fatalf("queue_wait_ms = %d, want 5000 (started_at − created_at)", rep.QueueWaitMs)
	}
	if rep.AttemptID != "lease-1" {
		t.Fatalf("attempt_id = %q, want lease-1", rep.AttemptID)
	}
	if rep.Counters.Retries != 2 {
		t.Fatalf("counters.retries = %d, want 2", rep.Counters.Retries)
	}

	// Nil StartedAt → zero queue wait (no panic).
	nilStart := obs.StartRunForClaim(context.Background(), ClaimRunInfo{
		JobID: "j2", JobType: "t", AttemptID: "attempt-j2", CreatedAt: createdAt, StartedAt: nil,
	})
	if rep2 := nilStart.Finish(); rep2.QueueWaitMs != 0 {
		t.Fatalf("queue_wait_ms with nil started_at = %d, want 0", rep2.QueueWaitMs)
	}
}

func TestRunObserver_RunSuccess(t *testing.T) {
	obs := NewRunObserver(nil)
	rep, err := obs.Run(context.Background(), RunInfo{JobID: "j", JobType: "t", AttemptID: "attempt-j"}, func(ctx context.Context) error {
		return MeasureStage(ctx, StageVerify, func(context.Context) error { return nil })
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Status != StatusSucceeded {
		t.Fatalf("status = %q, want SUCCEEDED", rep.Status)
	}
	if len(rep.Stages) != 1 {
		t.Fatalf("stages = %d, want 1 (context-bound stage)", len(rep.Stages))
	}
}

func TestRunObserver_RunErrorMarksFailed(t *testing.T) {
	obs := NewRunObserver(nil)
	rep, err := obs.Run(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"}, func(context.Context) error {
		return codedError{code: "engine_failed"}
	})
	if err == nil {
		t.Fatal("Run must propagate the body error")
	}
	if rep.Status != StatusFailed || rep.ErrorCode != "engine_failed" {
		t.Fatalf("status/error_code = %q/%q", rep.Status, rep.ErrorCode)
	}
}

func TestRunObserver_RunBodyPanicClosesRunAndRepanics(t *testing.T) {
	rec := &captureRecorder{}
	obs := NewRunObserver(rec)

	mustPanic(t, func() {
		_, _ = obs.Run(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"}, func(context.Context) error {
			panic("body boom")
		})
	})

	if rec.count() != 1 {
		t.Fatalf("persisted reports = %d, want 1 (run must be closed on panic)", rec.count())
	}
	if rep := rec.reports[0]; rep.Status != StatusFailed {
		t.Fatalf("run status = %q, want FAILED", rep.Status)
	}
}

// ── context ──────────────────────────────────────────────────────────

func TestWithRun_FromContextRoundTrip(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	ctx := WithRun(context.Background(), run)

	got, ok := RunFromContext(ctx)
	if !ok || got != run {
		t.Fatal("RunFromContext must return the bound run")
	}
	if FromContext(context.Background()) != nil {
		t.Fatal("FromContext must return nil without a bound run")
	}
	if FromContext(nil) != nil {
		t.Fatal("FromContext(nil) must return nil")
	}
}

// ── counters / artifacts / children / blocked ────────────────────────

func TestRun_Counters(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	run.AddItemsRequested(20)
	run.AddItemsCompleted(18)
	run.AddItemsFailed(2)
	run.IncCacheHit()
	run.IncCacheMiss()
	run.IncRetry()
	run.AddBytesDownloaded(1000)
	run.AddBytesUploaded(500)
	run.IncArtifactCreated()
	run.IncArtifactReused()

	c := run.Report().Counters
	if c.ItemsRequested != 20 || c.ItemsCompleted != 18 || c.ItemsFailed != 2 {
		t.Fatalf("items = %d/%d/%d", c.ItemsRequested, c.ItemsCompleted, c.ItemsFailed)
	}
	if c.CacheHits != 1 || c.CacheMisses != 1 || c.Retries != 1 {
		t.Fatalf("cache/retries = %d/%d/%d", c.CacheHits, c.CacheMisses, c.Retries)
	}
	if c.BytesDownloaded != 1000 || c.BytesUploaded != 500 {
		t.Fatalf("bytes = %d/%d", c.BytesDownloaded, c.BytesUploaded)
	}
	if c.ArtifactsCreated != 1 || c.ArtifactsReused != 1 {
		t.Fatalf("artifacts = %d/%d", c.ArtifactsCreated, c.ArtifactsReused)
	}
}

func TestRun_SetRetries(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})

	// Snapshot semantics: overwrite (not accumulate) — the value is the
	// canonical job.RetryCount at claim time.
	run.SetRetries(2)
	run.SetRetries(3)
	if got := run.Report().Counters.Retries; got != 3 {
		t.Fatalf("retries = %d, want 3 (last write wins)", got)
	}

	// Negative input is clamped.
	run.SetRetries(-5)
	if got := run.Report().Counters.Retries; got != 0 {
		t.Fatalf("retries = %d, want 0 after negative clamp", got)
	}
}

func TestRun_AddArtifact(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	run.AddArtifact(ArtifactReport{Kind: "drive_file", Ref: "file-1", Stage: string(StagePublish), Bytes: 42})
	run.AddArtifact(ArtifactReport{Kind: "drive_file", Ref: "file-2", Reused: true})

	rep := run.Report()
	if len(rep.Artifacts) != 2 || rep.Artifacts[0].Ref != "file-1" {
		t.Fatalf("artifacts = %#v", rep.Artifacts)
	}
	if rep.Counters.ArtifactsCreated != 1 || rep.Counters.ArtifactsReused != 1 {
		t.Fatalf("counters = %d/%d", rep.Counters.ArtifactsCreated, rep.Counters.ArtifactsReused)
	}
}

func TestRun_RegisterChildIsIdempotentAndUpdatesStatus(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "parent-job", RunID: "parent-run", AttemptID: "parent-attempt"})
	run.RegisterChild(&RunReport{RunID: "child-run", JobID: "child-job", Status: StatusRunning})
	run.RegisterChild(&RunReport{RunID: "child-run", JobID: "child-job", Status: StatusSucceeded, WallTimeMs: 25})
	rep := run.Report()
	if rep.Children == nil || rep.Children.Requested != 1 || rep.Children.Completed != 1 || rep.Children.Failed != 0 || rep.Children.AccumulatedChildMs != 25 {
		t.Fatalf("children = %#v", rep.Children)
	}
}

func TestRun_RegisterChildSummary(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	run.RegisterChild(&RunReport{Status: StatusSucceeded, WallTimeMs: 1000})
	run.RegisterChild(&RunReport{Status: StatusFailed, WallTimeMs: 500})

	rep := run.Report()
	if rep.Children == nil {
		t.Fatal("children summary must be populated")
	}
	if rep.Children.Requested != 2 || rep.Children.Completed != 1 || rep.Children.Failed != 1 {
		t.Fatalf("children = %d/%d/%d", rep.Children.Requested, rep.Children.Completed, rep.Children.Failed)
	}
	if rep.Children.AccumulatedChildMs != 1500 {
		t.Fatalf("children wall = %d, want 1500 (summed, not averaged)", rep.Children.AccumulatedChildMs)
	}
}

func TestRun_RecordWaitUsesTypedUnion(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-wait"})
	start := time.Unix(100, 0)
	run.RecordWait(WaitInfo{Kind: WaitSemaphore, Component: ComponentTTS, StartedAt: start, FinishedAt: start.Add(1500 * time.Millisecond)})
	run.RecordWait(WaitInfo{Kind: WaitRateLimit, Component: ComponentArtlist, StartedAt: start.Add(500 * time.Millisecond), FinishedAt: start.Add(2 * time.Second)})
	rep := run.Report()
	if rep.BlockedMs != 2000 {
		t.Fatalf("blocked_ms = %d, want 2000 (union of overlapping waits)", rep.BlockedMs)
	}
	if len(rep.Waits) != 2 || rep.Waits[0].Kind != WaitSemaphore || rep.Waits[1].Kind != WaitRateLimit {
		t.Fatalf("wait observations = %#v", rep.Waits)
	}
}

func TestRun_StartRequiresAttemptID(t *testing.T) {
	mustPanic(t, func() {
		NewRunObserver(nil).StartRun(context.Background(), RunInfo{JobID: "j"})
	})
}

// ── recorder sink ────────────────────────────────────────────────────

func TestRun_RecorderSinkCalledOnce(t *testing.T) {
	rec := &captureRecorder{}
	obs := NewRunObserver(rec)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	run.Finish()
	run.Finish()
	if rec.count() != 1 {
		t.Fatalf("sink calls = %d, want 1", rec.count())
	}
	if rec.reports[0].Status != StatusSucceeded {
		t.Fatalf("sink report status = %q", rec.reports[0].Status)
	}
}

func TestRun_CollectorSinkCalledOnce(t *testing.T) {
	collector := &captureCollector{mutate: true}
	obs := NewRunObserverWithCollector(nil, collector)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})
	run.Finish()
	run.Finish()
	if collector.count() != 1 {
		t.Fatalf("collector calls = %d, want 1", collector.count())
	}
	if collector.reports[0].Status != "MUTATED" {
		t.Fatalf("collector report status = %q", collector.reports[0].Status)
	}
	if run.Report().Status != StatusSucceeded {
		t.Fatalf("collector mutation leaked into run report: %q", run.Report().Status)
	}
}

// ── JSON ─────────────────────────────────────────────────────────────

func TestRunReport_JSONRoundTrip(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{
		RunID: "r1", JobID: "j1", JobType: "script.generate", AttemptID: "attempt-r1", QueueWaitMs: 450,
	})
	_ = run.Stage(context.Background(), StageAcquire, func(context.Context) error { return nil })
	run.Finish()

	raw, err := run.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{
		"run_id", "job_id", "job_type", "status",
		"created_at", "started_at", "finished_at",
		"attempt_id", "queue_wait_ms", "wall_time_ms",
		"stages", "counters",
	} {
		if _, ok := m[key]; !ok {
			t.Errorf("JSON is missing key %q", key)
		}
	}
	if m["run_id"] != "r1" || m["status"] != StatusSucceeded {
		t.Fatalf("envelope = %v/%v", m["run_id"], m["status"])
	}
	if _, ok := m["active_ms"]; ok {
		t.Fatal("active_ms must be omitted until active interval union is implemented")
	}
}

// ── concurrency ──────────────────────────────────────────────────────

func TestRun_ConcurrentOperations(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = run.Operation(context.Background(), OperationInfo{
				Stage: StageResolve, Component: ComponentQdrant, Operation: OperationSearch,
			}, func(context.Context) error { return nil })
		}()
	}
	wg.Wait()

	rep := run.Report()
	if len(rep.Operations) != n {
		t.Fatalf("operations = %d, want %d", len(rep.Operations), n)
	}
}

func TestRun_ConcurrentCounters(t *testing.T) {
	obs := NewRunObserver(nil)
	run := obs.StartRun(context.Background(), RunInfo{JobID: "j", AttemptID: "attempt-j"})

	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run.AddItemsCompleted(1)
			run.IncCacheHit()
		}()
	}
	wg.Wait()

	c := run.Report().Counters
	if c.ItemsCompleted != n || c.CacheHits != n {
		t.Fatalf("counters = %d/%d, want %d", c.ItemsCompleted, c.CacheHits, n)
	}
}

// ── registry ─────────────────────────────────────────────────────────

func TestRegistry_CanonicalNames(t *testing.T) {
	if len(AllStages()) != 12 {
		t.Fatalf("stages = %d, want 12", len(AllStages()))
	}
	if len(AllComponents()) != 12 {
		t.Fatalf("components = %d, want 12", len(AllComponents()))
	}
	if len(AllOperations()) != 21 {
		t.Fatalf("operations = %d, want 21", len(AllOperations()))
	}
	if string(StageAcquire) != "acquire" || string(ComponentQdrant) != "qdrant" || string(OperationUpsert) != "upsert" {
		t.Fatal("registry literals drifted from the canonical strings")
	}
}
