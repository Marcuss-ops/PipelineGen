// Package scriptgeneration — final_job_pending_test.go pins the split remote
// final-job handoff: the wait is no longer one blocking PREPARE→FINALIZE→poll
// call that owns a worker for the whole remote render. An attempt that finds
// the render still running persists the Master job id and hands the wait back;
// the attempt that follows RESUMES that job (no second PREPARE) and, because the
// first wait was already yielded once, runs it to completion — so a render
// longer than the entire retry window still finishes instead of dying on an
// exhausted attempt budget.
package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
)

type attachCall struct {
	jobID            string
	waitToCompletion bool
}

// stubFinalJobSubmitter is the submit-only half of the port (the wiring every
// run that is not a final-job run uses).
type stubFinalJobSubmitter struct {
	submits int
	result  RemoteFinalJobResult
	err     error
}

func (s *stubFinalJobSubmitter) SubmitFinalJob(context.Context, string, GenerateRequest, *GenerateResult) (RemoteFinalJobResult, error) {
	s.submits++
	return s.result, s.err
}

// stubFinalJobAttacher adds the OPTIONAL resume half and records how it was
// called, so a test can prove the runner resumed a job instead of submitting.
type stubFinalJobAttacher struct {
	stubFinalJobSubmitter
	attachCalls  []attachCall
	attachResult RemoteFinalJobResult
	attachErr    error
}

func (s *stubFinalJobAttacher) AttachFinalJob(_ context.Context, jobID string, waitToCompletion bool) (RemoteFinalJobResult, error) {
	s.attachCalls = append(s.attachCalls, attachCall{jobID: jobID, waitToCompletion: waitToCompletion})
	return s.attachResult, s.attachErr
}

func newFinalJobRunner(submitter FinalJobSubmitter, runID string, result *GenerateResult) (*Runner, *inMemRunRepository) {
	repo := newInMemRunRepository()
	_ = repo.Create(context.Background(), &GenerationRun{ID: runID, JobID: runID, Status: RunStatusRunning, Result: result})
	runner := &Runner{repo: repo, log: zap.NewNop(), finalJobSubmitter: submitter}
	return runner, repo
}

// TestPendingRemoteRenderYieldsWithADurableHandle is the core of the split: a
// wait that outlives the attempt is NOT a failure and NOT a lost handle. The
// run ends with the deferral error code, and the Master job id survives in the
// durable partial result so the next attempt can resume it.
func TestPendingRemoteRenderYieldsWithADurableHandle(t *testing.T) {
	const runID = "run-pending"
	submitter := &stubFinalJobSubmitter{
		result: RemoteFinalJobResult{JobID: "master-1", Status: "RUNNING", WorkerID: "w-9"},
		err:    fmt.Errorf("remote job master-1 is still RUNNING: %w", ErrFinalJobPending),
	}
	result := &GenerateResult{}
	runner, repo := newFinalJobRunner(submitter, runID, nil)

	if ok := runner.submitFinalJob(context.Background(), runID, defaultTestRequest(), result); ok {
		t.Fatal("a pending remote render must not report success")
	}
	if submitter.submits != 1 {
		t.Fatalf("submit calls = %d, want 1", submitter.submits)
	}

	receipt := result.RemoteFinalJob
	if receipt == nil {
		t.Fatal("the in-memory result must carry the remote receipt")
	}
	if receipt.JobID != "master-1" || receipt.Status != "RUNNING" {
		t.Fatalf("receipt = %+v, want the Master handle and its raw status", receipt)
	}
	if receipt.Yields != 1 {
		t.Fatalf("Yields = %d, want 1 (this attempt handed the wait back)", receipt.Yields)
	}

	run, err := repo.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.Status != RunStatusFailed {
		t.Fatalf("run status = %q, want FAILED (the attempt ended)", run.Status)
	}
	if run.ErrorCode != "REMOTE_RENDER_PENDING" {
		t.Fatalf("error code = %q, want REMOTE_RENDER_PENDING", run.ErrorCode)
	}
	if run.FailedStage != StagePublishingDocuments {
		t.Fatalf("failed stage = %q, want %q", run.FailedStage, StagePublishingDocuments)
	}
	if run.NextRetryAt == nil {
		t.Fatal("a deferral must schedule a retry: NextRetryAt is nil")
	}
	if run.Result == nil || run.Result.RemoteFinalJob == nil {
		t.Fatal("the durable result must carry the receipt before the attempt ends")
	}
	if run.Result.RemoteFinalJob.JobID != "master-1" || run.Result.RemoteFinalJob.Yields != 1 {
		t.Fatalf("durable receipt = %+v, want the Master handle with Yields=1", run.Result.RemoteFinalJob)
	}
}

// TestDeferredAttemptResumesTheJobInsteadOfSubmittingASecondRender: a retried
// run must attach to the job it already started. A second PREPARE would ask the
// Master for a second render of work it is already doing.
func TestDeferredAttemptResumesTheJobInsteadOfSubmittingASecondRender(t *testing.T) {
	const runID = "run-resume"
	submitter := &stubFinalJobAttacher{
		attachResult: RemoteFinalJobResult{JobID: "master-7", Status: "SUCCEEDED", ArtifactURL: "velox-drive://final", SHA256: "abc"},
	}
	result := &GenerateResult{RemoteFinalJob: &RemoteFinalJobResult{JobID: "master-7", Status: "RUNNING", Yields: 0}}
	runner, repo := newFinalJobRunner(submitter, runID, result)

	if ok := runner.submitFinalJob(context.Background(), runID, defaultTestRequest(), result); !ok {
		t.Fatal("the resumed wait completed the remote render; submitFinalJob must report success")
	}
	if submitter.submits != 0 {
		t.Fatalf("submit calls = %d, want 0 — a resumed run must never PREPARE again", submitter.submits)
	}
	if len(submitter.attachCalls) != 1 {
		t.Fatalf("attach calls = %v, want exactly 1", submitter.attachCalls)
	}
	if call := submitter.attachCalls[0]; call.jobID != "master-7" || call.waitToCompletion {
		t.Fatalf("attach call = %+v, want the first bounded wait on master-7", call)
	}
	run, err := repo.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.Result.RemoteFinalJob.Status != "SUCCEEDED" {
		t.Fatalf("durable receipt = %+v, want the terminal SUCCEEDED status", run.Result.RemoteFinalJob)
	}
}

// TestDurableReceiptOutlivesAReplayedResult: an attempt that rebuilt its result
// (rather than adopting the checkpoint) must still see the handle the run has
// persisted. Otherwise the duplicate-render guard would depend on which resume
// path the run happened to take.
func TestDurableReceiptOutlivesAReplayedResult(t *testing.T) {
	const runID = "run-replayed"
	persisted := &GenerateResult{RemoteFinalJob: &RemoteFinalJobResult{JobID: "master-durable", Status: "RUNNING"}}
	submitter := &stubFinalJobAttacher{
		attachResult: RemoteFinalJobResult{JobID: "master-durable", Status: "SUCCEEDED"},
	}
	runner, _ := newFinalJobRunner(submitter, runID, persisted)

	// A fresh in-memory result, exactly like a replay that regenerated scenes.
	result := &GenerateResult{}
	if ok := runner.submitFinalJob(context.Background(), runID, defaultTestRequest(), result); !ok {
		t.Fatal("the durable receipt is authoritative: the step must resume, not fail")
	}
	if submitter.submits != 0 {
		t.Fatalf("submit calls = %d, want 0 — the run already has a render in flight", submitter.submits)
	}
	if len(submitter.attachCalls) != 1 || submitter.attachCalls[0].jobID != "master-durable" {
		t.Fatalf("attach calls = %+v, want one attach on the persisted handle", submitter.attachCalls)
	}
	if result.RemoteFinalJob == nil || result.RemoteFinalJob.Status != "SUCCEEDED" {
		t.Fatalf("result receipt = %+v, want the terminal receipt recorded", result.RemoteFinalJob)
	}
}

// TestSecondWaitRunsToCompletion pins the churn bound: the wait is handed back
// at most once per run. From the second wait on, the job is waited out with the
// submitter's full poll timeout, so a render longer than the retry window still
// completes.
func TestSecondWaitRunsToCompletion(t *testing.T) {
	const runID = "run-second-wait"
	submitter := &stubFinalJobAttacher{
		attachResult: RemoteFinalJobResult{JobID: "master-3", Status: "RUNNING"},
		attachErr:    fmt.Errorf("remote job master-3 still RUNNING after 2h: %w", ErrFinalJobPending),
	}
	result := &GenerateResult{RemoteFinalJob: &RemoteFinalJobResult{JobID: "master-3", Status: "RUNNING", Yields: 1}}
	runner, repo := newFinalJobRunner(submitter, runID, result)

	if ok := runner.submitFinalJob(context.Background(), runID, defaultTestRequest(), result); ok {
		t.Fatal("a still-pending render is not a success")
	}
	if submitter.submits != 0 {
		t.Fatalf("submit calls = %d, want 0", submitter.submits)
	}
	if len(submitter.attachCalls) != 1 || !submitter.attachCalls[0].waitToCompletion {
		t.Fatalf("attach calls = %+v, want one wait-to-completion call", submitter.attachCalls)
	}
	run, err := repo.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := run.Result.RemoteFinalJob.Yields; got != 2 {
		t.Fatalf("Yields = %d, want 2 (every yielded wait is counted durably)", got)
	}
}

// TestCompletedRemoteReceiptIsNeverResubmitted: the render finished but a later
// phase failed the run. Re-submitting would duplicate work the Master already
// did, so the receipt is authoritative and the step is a no-op.
func TestCompletedRemoteReceiptIsNeverResubmitted(t *testing.T) {
	const runID = "run-already-rendered"
	submitter := &stubFinalJobAttacher{}
	result := &GenerateResult{RemoteFinalJob: &RemoteFinalJobResult{JobID: "master-42", Status: "SUCCEEDED", ArtifactURL: "velox-drive://done"}}
	runner, repo := newFinalJobRunner(submitter, runID, result)

	if ok := runner.submitFinalJob(context.Background(), runID, defaultTestRequest(), result); !ok {
		t.Fatal("an already completed remote render must not fail the step again")
	}
	if submitter.submits != 0 || len(submitter.attachCalls) != 0 {
		t.Fatalf("submit=%d attach=%v, want no Master traffic at all", submitter.submits, submitter.attachCalls)
	}
	run, err := repo.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.Status == RunStatusFailed {
		t.Fatalf("run status = %q, want it untouched", run.Status)
	}
}

// TestPendingReceiptWithoutAResumableSubmitterFailsClosed: a submitter that
// cannot wait on an existing job must not fall back to submitting, because the
// only way out of that fallback is a duplicate render.
func TestPendingReceiptWithoutAResumableSubmitterFailsClosed(t *testing.T) {
	const runID = "run-not-resumable"
	submitter := &stubFinalJobSubmitter{result: RemoteFinalJobResult{JobID: "should-not-be-used", Status: "SUCCEEDED"}}
	result := &GenerateResult{RemoteFinalJob: &RemoteFinalJobResult{JobID: "master-5", Status: "RUNNING"}}
	runner, repo := newFinalJobRunner(submitter, runID, result)

	if ok := runner.submitFinalJob(context.Background(), runID, defaultTestRequest(), result); ok {
		t.Fatal("an unresumable in-flight render must fail closed")
	}
	if submitter.submits != 0 {
		t.Fatalf("submit calls = %d, want 0 — submitting here duplicates the render", submitter.submits)
	}
	run, err := repo.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.Status != RunStatusFailed || !containsAny(run.ErrorMessage, "cannot resume") {
		t.Fatalf("run = %+v, want a failure that names the unresumable job", run)
	}
}

// TestPendingRemoteRenderErrorCodeIsStable pins the deferral code against the
// message heuristics: the wrapped transport message says "timeout", which would
// otherwise classify a deliberate deferral as PROVIDER_TIMEOUT.
func TestPendingRemoteRenderErrorCodeIsStable(t *testing.T) {
	err := fmt.Errorf("remote final job master-1 is still RUNNING: %w: %w",
		ErrFinalJobPending, context.DeadlineExceeded)
	if got := deriveErrorCode(err, StagePublishingDocuments); got != "REMOTE_RENDER_PENDING" {
		t.Fatalf("error code = %q, want REMOTE_RENDER_PENDING", got)
	}
	if got := deriveErrorCode(errors.New("boom"), StagePublishingDocuments); got == "REMOTE_RENDER_PENDING" {
		t.Fatal("an unrelated failure must not borrow the deferral code")
	}
}

// TestDeferredRemoteRenderResumesThroughTheFullLadder is the end-to-end proof
// of the split on the REAL pipeline (every phase, driven exactly as the worker
// drives it): the first attempt runs the whole ladder, defers at the remote
// render, and the next attempt resumes the same run and attaches to the job it
// already started — without a second PREPARE.
func TestDeferredRemoteRenderResumesThroughTheFullLadder(t *testing.T) {
	const runID = "run-ladder-final-job"
	runner, repo, _, _, _, _, _ := newTestRunner()
	req := defaultTestRequest()
	req.GenerateTimeline = true
	req.FinalJob = true

	// Attempt 1: the Master keeps rendering past this attempt's budget.
	submitter := &stubFinalJobAttacher{
		stubFinalJobSubmitter: stubFinalJobSubmitter{
			result: RemoteFinalJobResult{JobID: "master-ladder", Status: "RUNNING"},
			err:    fmt.Errorf("remote job master-ladder is still RUNNING: %w", ErrFinalJobPending),
		},
	}
	runner.SetFinalJobSubmitter(submitter)
	if err := repo.Create(context.Background(), &GenerationRun{
		ID: runID, JobID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	runner.Execute(context.Background(), runID, req)

	deferred := awaitCompletion(t, repo, runID, 5*time.Second)
	if deferred.Status != RunStatusFailed {
		t.Fatalf("attempt 1 status = %q, want FAILED on a deferred render", deferred.Status)
	}
	if deferred.ErrorCode != "REMOTE_RENDER_PENDING" {
		t.Fatalf("attempt 1 error code = %q, want REMOTE_RENDER_PENDING", deferred.ErrorCode)
	}
	if submitter.submits != 1 {
		t.Fatalf("attempt 1 submits = %d, want 1", submitter.submits)
	}
	if deferred.Result == nil || deferred.Result.RemoteFinalJob == nil || deferred.Result.RemoteFinalJob.JobID != "master-ladder" {
		t.Fatalf("attempt 1 durable result = %+v, want the Master handle", deferred.Result)
	}

	// Attempt 2 — exactly what the retry of the SAME run does: re-execute the
	// job against the run the first attempt left FAILED, so the resume lands on
	// the failed stage and reaches the final-job step again.
	submitter.attachResult = RemoteFinalJobResult{JobID: "master-ladder", Status: "SUCCEEDED", ArtifactURL: "velox-drive://final"}
	runner.Execute(context.Background(), runID, req)

	completed := awaitCompletion(t, repo, runID, 5*time.Second)
	if completed.Status != RunStatusCompleted {
		t.Fatalf("attempt 2 status = %q (code=%q msg=%q), want COMPLETED", completed.Status, completed.ErrorCode, completed.ErrorMessage)
	}
	if submitter.submits != 1 {
		t.Fatalf("total submits = %d, want 1 — attempt 2 must attach, not resubmit", submitter.submits)
	}
	if len(submitter.attachCalls) != 1 {
		t.Fatalf("attach calls = %v, want exactly 1", submitter.attachCalls)
	}
	if call := submitter.attachCalls[0]; call.jobID != "master-ladder" || !call.waitToCompletion {
		t.Fatalf("attach call = %+v, want the second wait to run to completion", call)
	}
	if completed.Result == nil || completed.Result.RemoteFinalJob == nil || completed.Result.RemoteFinalJob.Status != "SUCCEEDED" {
		t.Fatalf("final durable result = %+v, want the terminal receipt", completed.Result)
	}
}

// TestFirstBoundedWaitIsTheOnlyOneThatYields is the budget-side complement of
// the attach tests: the very first wait (the submit path) carries no prior
// yield, and the receipt it produces is what makes the next attempt unbounded.
func TestFirstBoundedWaitIsTheOnlyOneThatYields(t *testing.T) {
	const runID = "run-first-wait"
	submitter := &stubFinalJobAttacher{
		stubFinalJobSubmitter: stubFinalJobSubmitter{
			result: RemoteFinalJobResult{JobID: "master-11", Status: "RUNNING"},
			err:    fmt.Errorf("remote job master-11 still RUNNING after 60s: %w", ErrFinalJobPending),
		},
	}
	result := &GenerateResult{}
	runner, repo := newFinalJobRunner(submitter, runID, nil)

	if ok := runner.submitFinalJob(context.Background(), runID, defaultTestRequest(), result); ok {
		t.Fatal("want a deferral, not success")
	}
	// The submit path is the bounded one; no attach happens on a first attempt.
	if len(submitter.attachCalls) != 0 {
		t.Fatalf("attach calls = %v, want none on a first attempt", submitter.attachCalls)
	}
	run, err := repo.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.Result.RemoteFinalJob.Yields != 1 {
		t.Fatalf("Yields = %d, want 1", run.Result.RemoteFinalJob.Yields)
	}
	if !run.NextRetryAt.After(time.Now().UTC().Add(-time.Second)) {
		t.Fatalf("NextRetryAt = %v, want a future retry", run.NextRetryAt)
	}
}
