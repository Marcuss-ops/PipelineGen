package reconciliation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type runStoreStub struct {
	candidates []scriptgen.StaleSubmissionRun
	linked     map[string]string
	failures   []scriptgen.FailRunInput
	completed  []string
	runs       map[string]*scriptgen.GenerationRun
	listErr    error
}

func (s *runStoreStub) ListStaleSubmissionRuns(_ context.Context, _ time.Time, _ int) ([]scriptgen.StaleSubmissionRun, error) {
	return s.candidates, s.listErr
}
func (s *runStoreStub) SetJobID(_ context.Context, runID, jobID string) error {
	if s.linked == nil {
		s.linked = map[string]string{}
	}
	s.linked[runID] = jobID
	return nil
}
func (s *runStoreStub) Get(_ context.Context, runID string) (*scriptgen.GenerationRun, error) {
	return s.runs[runID], nil
}
func (s *runStoreStub) UpdateStage(_ context.Context, runID string, status scriptgen.RunStatus, stage scriptgen.Stage) error {
	if status != scriptgen.RunStatusCompleted || stage != scriptgen.StageCompleted {
		return errors.New("unexpected stage projection")
	}
	s.completed = append(s.completed, runID)
	return nil
}
func (s *runStoreStub) FailRun(_ context.Context, input scriptgen.FailRunInput) error {
	s.failures = append(s.failures, input)
	return nil
}

type jobReaderStub struct {
	jobs []job.Job
	byID *job.Job
	err  error
}

func (s jobReaderStub) List(_ context.Context, filter job.Filter) ([]job.Job, error) {
	if s.err != nil {
		return nil, s.err
	}
	if filter.Type == nil || *filter.Type != job.TypeScriptGenerate || filter.CorrelationID == nil || filter.Limit != 2 {
		return nil, errors.New("reconciler supplied an incomplete job lookup")
	}
	return s.jobs, nil
}
func (s jobReaderStub) Get(_ context.Context, id string) (*job.Job, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.byID == nil || s.byID.ID != id {
		return nil, nil
	}
	return s.byID, nil
}

func TestReconcileLinksAcceptedJobByIdempotencyCorrelation(t *testing.T) {
	store := &runStoreStub{candidates: []scriptgen.StaleSubmissionRun{{
		RunID: "run-1", JobID: "run-1", IdempotencyKey: "idem-1", CurrentStage: scriptgen.StagePreflight,
	}}}
	reconciler := Reconciler{Runs: store, Jobs: jobReaderStub{jobs: []job.Job{{
		ID: "job-1", Type: job.TypeScriptGenerate, CorrelationID: "idem-1", Status: job.StatusQueued,
	}}}}

	report, err := reconciler.Reconcile(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Inspected != 1 || report.Linked != 1 || report.Deferred != 1 || len(store.failures) != 0 || store.linked["run-1"] != "job-1" {
		t.Fatalf("unexpected reconciliation outcome: report=%+v linked=%v failures=%+v", report, store.linked, store.failures)
	}
}

func TestReconcileFailsOldRunWhenNoJobWasCommitted(t *testing.T) {
	store := &runStoreStub{candidates: []scriptgen.StaleSubmissionRun{{
		RunID: "run-orphan", JobID: "run-orphan", IdempotencyKey: "idem-orphan", CurrentStage: scriptgen.StageGeneratingSceneText,
	}}}
	reconciler := Reconciler{Runs: store, Jobs: jobReaderStub{}}

	report, err := reconciler.Reconcile(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 || len(store.failures) != 1 {
		t.Fatalf("expected one stale orphan to fail, report=%+v failures=%+v", report, store.failures)
	}
	failure := store.failures[0]
	if failure.RunID != "run-orphan" || failure.ErrorCode != "SUBMISSION_ORPHANED" || failure.FailedStage != scriptgen.StageGeneratingSceneText {
		t.Fatalf("failure did not preserve run identity/stage: %+v", failure)
	}
}

func TestReconcileLeavesRunUntouchedWhenJobLookupIsUnavailable(t *testing.T) {
	store := &runStoreStub{candidates: []scriptgen.StaleSubmissionRun{{
		RunID: "run-retry", JobID: "run-retry", IdempotencyKey: "idem-retry",
	}}}
	reconciler := Reconciler{Runs: store, Jobs: jobReaderStub{err: errors.New("broker unavailable")}}

	report, err := reconciler.Reconcile(context.Background(), time.Now())
	if err == nil || report.Inspected != 1 || len(store.failures) != 0 || len(store.linked) != 0 {
		t.Fatalf("broker outage must be retryable without mutation: report=%+v err=%v linked=%v failures=%+v", report, err, store.linked, store.failures)
	}
}

func TestReconcileRefusesAmbiguousCorrelation(t *testing.T) {
	store := &runStoreStub{candidates: []scriptgen.StaleSubmissionRun{{
		RunID: "run-ambiguous", JobID: "run-ambiguous", IdempotencyKey: "idem-ambiguous",
	}}}
	reconciler := Reconciler{Runs: store, Jobs: jobReaderStub{jobs: []job.Job{
		{ID: "job-a", Type: job.TypeScriptGenerate, CorrelationID: "idem-ambiguous"},
		{ID: "job-b", Type: job.TypeScriptGenerate, CorrelationID: "idem-ambiguous"},
	}}}

	report, err := reconciler.Reconcile(context.Background(), time.Now())
	if err == nil || report.Ambiguous != 1 || len(store.linked) != 0 || len(store.failures) != 0 {
		t.Fatalf("ambiguous correlation must fail closed: report=%+v err=%v linked=%v failures=%+v", report, err, store.linked, store.failures)
	}
}

func TestReconcileRepairsTerminalJobOutcomeAndResult(t *testing.T) {
	store := &runStoreStub{candidates: []scriptgen.StaleSubmissionRun{{
		RunID: "run-terminal", JobID: "job-terminal", IdempotencyKey: "idem-terminal", CurrentStage: scriptgen.StageCompilingAudio,
	}}, runs: map[string]*scriptgen.GenerationRun{"run-terminal": {
		ID: "run-terminal", Result: &scriptgen.GenerateResult{Title: "checkpoint-result"},
	}}}
	remoteJob := &job.Job{
		ID: "job-terminal", Type: job.TypeScriptGenerate, CorrelationID: "idem-terminal", Status: job.StatusSucceeded,
		// The broker stores the artifact manifest for artifact-producing jobs,
		// not the GenerateResult; reconciliation must use the run checkpoint.
		Result: json.RawMessage(`{"data":{"artifacts":[]}}`),
	}
	reconciler := Reconciler{Runs: store, Jobs: jobReaderStub{byID: remoteJob}}

	report, err := reconciler.Reconcile(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Completed != 1 || store.runs["run-terminal"].Result.Title != "checkpoint-result" || len(store.completed) != 1 {
		t.Fatalf("terminal job result was not projected to run: report=%+v run=%+v completed=%v", report, store.runs["run-terminal"], store.completed)
	}
}

func TestReconcileDoesNotInventMissingTerminalRunResult(t *testing.T) {
	store := &runStoreStub{
		candidates: []scriptgen.StaleSubmissionRun{{RunID: "run-no-result", JobID: "job-no-result", IdempotencyKey: "idem-no-result"}},
		runs:       map[string]*scriptgen.GenerationRun{"run-no-result": {ID: "run-no-result"}},
	}
	reconciler := Reconciler{Runs: store, Jobs: jobReaderStub{byID: &job.Job{
		ID: "job-no-result", Type: job.TypeScriptGenerate, CorrelationID: "idem-no-result", Status: job.StatusSucceeded,
		Result: json.RawMessage(`{"data":{"artifacts":[]}}`),
	}}}

	report, err := reconciler.Reconcile(context.Background(), time.Now())
	if err == nil || report.Completed != 0 || len(store.completed) != 0 {
		t.Fatalf("missing run result must remain visible instead of being fabricated: report=%+v err=%v completed=%v", report, err, store.completed)
	}
}

func TestReconcileRepairsTerminalFailure(t *testing.T) {
	store := &runStoreStub{candidates: []scriptgen.StaleSubmissionRun{{
		RunID: "run-failed", JobID: "job-failed", IdempotencyKey: "idem-failed", CurrentStage: scriptgen.StageGeneratingVoiceovers,
	}}}
	reconciler := Reconciler{Runs: store, Jobs: jobReaderStub{byID: &job.Job{
		ID: "job-failed", Type: job.TypeScriptGenerate, CorrelationID: "idem-failed", Status: job.StatusFailed, Error: "worker failed",
	}}}

	report, err := reconciler.Reconcile(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 || len(store.failures) != 1 || store.failures[0].ErrorCode != "JOB_FAILED" || store.failures[0].FailedStage != scriptgen.StageGeneratingVoiceovers {
		t.Fatalf("terminal failure was not projected: report=%+v failures=%+v", report, store.failures)
	}
}
