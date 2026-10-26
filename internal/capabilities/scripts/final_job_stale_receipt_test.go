package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// stubStaleThenFresh is the remote hand-off double for the stale-identity
// recovery path: the first StartFinalJob reports a preparation the Master has
// canceled (the `job_already_leased` class of refusal), the next one starts a
// fresh remote job and completes. It records the durable attempt intent each
// StartFinalJob received, which is what feeds finalJobAttemptIdempotencyKey.
type stubStaleThenFresh struct {
	startCalls   int
	attempts     []int
	startResults []RemoteFinalJobResult
	startErrs    []error
	attachCalls  []attachCall
	attachResult RemoteFinalJobResult
}

// SubmitFinalJob satisfies FinalJobSubmitter; the runner prefers the
// starter/attacher split, so the single-shot half delegates to StartFinalJob.
func (s *stubStaleThenFresh) SubmitFinalJob(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult) (RemoteFinalJobResult, error) {
	return s.StartFinalJob(ctx, runID, req, result)
}

func (s *stubStaleThenFresh) StartFinalJob(_ context.Context, _ string, _ GenerateRequest, result *GenerateResult) (RemoteFinalJobResult, error) {
	attempt := 0
	if result != nil && result.RemoteFinalJob != nil {
		attempt = result.RemoteFinalJob.Attempt
	}
	s.attempts = append(s.attempts, attempt)
	index := s.startCalls
	s.startCalls++
	if index >= len(s.startResults) {
		return RemoteFinalJobResult{}, fmt.Errorf("unexpected StartFinalJob call #%d", index+1)
	}
	return s.startResults[index], s.startErrs[index]
}

func (s *stubStaleThenFresh) AttachFinalJob(_ context.Context, jobID string, waitToCompletion bool) (RemoteFinalJobResult, error) {
	s.attachCalls = append(s.attachCalls, attachCall{jobID: jobID, waitToCompletion: waitToCompletion})
	return s.attachResult, nil
}

// staleRecoveryStub returns the double: attempt 1 dies on the canceled
// preparation, attempt 2 starts master-fresh and completes on attach.
func staleRecoveryStub() *stubStaleThenFresh {
	return &stubStaleThenFresh{
		startResults: []RemoteFinalJobResult{
			{JobID: "master-cancelled", Status: "CANCELED"},
			{JobID: "master-fresh", Status: "RUNNING"},
		},
		startErrs: []error{
			fmt.Errorf("remote preparation master-cancelled is stale and must be replaced: %w: remote job master-cancelled is stale (status=CANCELED)", ErrFinalJobStaleReceipt),
			nil,
		},
		attachResult: RemoteFinalJobResult{JobID: "master-fresh", Status: "SUCCEEDED", ArtifactURL: "velox-drive://final"},
	}
}

// TestStaleRemotePreparationIsArchivedAndRotatedByTheNextAttempt pins the
// Garlasco retry fix: a preparation the Master has canceled (or leased
// elsewhere) is archived as a TERMINAL receipt and the next attempt starts a
// FRESH remote identity. Replaying the dead one can never take a FINALIZE —
// the identity lives on the Master, so clearing the local receipt alone would
// loop on `job_already_leased` forever.
func TestStaleRemotePreparationIsArchivedAndRotatedByTheNextAttempt(t *testing.T) {
	const runID = "run-stale-rotate"
	submitter := staleRecoveryStub()
	result := &GenerateResult{}
	runner, repo := newFinalJobRunner(submitter, runID, nil)

	if ok := runner.submitFinalJob(context.Background(), runID, defaultTestRequest(), result); ok {
		t.Fatal("a stale remote preparation must not report success")
	}
	run, err := repo.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.Status != RunStatusFailed || run.ErrorCode != "REMOTE_JOB_STALE" {
		t.Fatalf("run = %+v, want FAILED with the stable REMOTE_JOB_STALE code", run)
	}
	if run.NextRetryAt == nil {
		t.Fatal("a stale identity is recovered by retrying with a fresh one: NextRetryAt must be set")
	}
	if run.Result == nil || run.Result.RemoteFinalJob == nil {
		t.Fatal("the canceled identity must be archived in the durable result")
	}
	if got := run.Result.RemoteFinalJob.Status; !finalJobReceiptTerminal(run.Result.RemoteFinalJob) {
		t.Fatalf("durable receipt status = %q, want a terminal status so the next attempt rotates", got)
	}

	if ok := runner.submitFinalJob(context.Background(), runID, defaultTestRequest(), result); !ok {
		t.Fatal("the rotated attempt must complete the fresh remote job")
	}
	if submitter.startCalls != 2 {
		t.Fatalf("StartFinalJob calls = %d, want 2 — the stale identity is never reused", submitter.startCalls)
	}
	if want := []int{1, 2}; len(submitter.attempts) != 2 || submitter.attempts[0] != want[0] || submitter.attempts[1] != want[1] {
		t.Fatalf("attempt intents = %v, want [1 2] — the second PREPARE carries the rotated attempt", submitter.attempts)
	}
	if len(result.RemoteFinalJobHistory) != 1 || result.RemoteFinalJobHistory[0].JobID != "master-cancelled" {
		t.Fatalf("attempt history = %+v, want the canceled identity archived", result.RemoteFinalJobHistory)
	}
	if result.RemoteFinalJob == nil || result.RemoteFinalJob.Status != "SUCCEEDED" || result.RemoteFinalJob.JobID != "master-fresh" {
		t.Fatalf("final receipt = %+v, want the fresh job completed", result.RemoteFinalJob)
	}
	if len(submitter.attachCalls) != 1 || submitter.attachCalls[0].jobID != "master-fresh" {
		t.Fatalf("attach calls = %+v, want one attach on the fresh identity", submitter.attachCalls)
	}
}

// TestStaleRemoteIdentityRotatesThePreparedIdempotencyKey pins the wire half
// of the rotation: the PREPARE and FINALIZE idempotency keys embed the durable
// attempt, so a retry after a stale preparation asks the Master for a NEW
// remote job instead of replaying the leased/canceled identifier.
func TestStaleRemoteIdentityRotatesThePreparedIdempotencyKey(t *testing.T) {
	build := func(attempt int) (map[string]any, map[string]any) {
		result := phraseOnlyFinalJobResult("scene-phrase")
		result.RemoteFinalJob = &RemoteFinalJobResult{Status: "PREPARE_INTENT", Attempt: attempt}
		pre, finalize, err := BuildFinalJobPayloads(context.Background(), "run-phrase", GenerateRequest{Title: "Garlasco", SourceLanguage: "it"}, result, finalJobPayloadResolver{})
		if err != nil {
			t.Fatalf("BuildFinalJobPayloads (attempt %d): %v", attempt, err)
		}
		return pre, finalize
	}
	preOne, finalizeOne := build(1)
	preTwo, finalizeTwo := build(2)

	keyOne, _ := preOne["idempotency_key"].(string)
	keyTwo, _ := preTwo["idempotency_key"].(string)
	if keyOne == "" || keyOne == keyTwo {
		t.Fatalf("PREPARE idempotency keys = %q vs %q, want one rotated key per attempt", keyOne, keyTwo)
	}
	if !strings.Contains(keyOne, "-attempt-1") || !strings.Contains(keyTwo, "-attempt-2") {
		t.Fatalf("PREPARE keys = %q, %q, want the attempt encoded in each", keyOne, keyTwo)
	}
	finalizeKeyOne, _ := finalizeOne["idempotency_key"].(string)
	finalizeKeyTwo, _ := finalizeTwo["idempotency_key"].(string)
	if finalizeKeyOne == finalizeKeyTwo || finalizeKeyOne != keyOne+"-finalize" || finalizeKeyTwo != keyTwo+"-finalize" {
		t.Fatalf("FINALIZE keys = %q vs %q, want one rotated -finalize key per attempt", finalizeKeyOne, finalizeKeyTwo)
	}
}

// TestStaleRemoteReceiptErrorCodeIsStable pins the operator-facing code: a
// retry bot must be able to tell "this attempt will rotate the remote
// identity" apart from a render failure or the pending deferral.
func TestStaleRemoteReceiptErrorCodeIsStable(t *testing.T) {
	err := fmt.Errorf("remote final job: %w: remote FINALIZE job master-1: HTTP 409: job_already_leased", ErrFinalJobStaleReceipt)
	if got := deriveErrorCode(err, StagePublishingDocuments); got != "REMOTE_JOB_STALE" {
		t.Fatalf("error code = %q, want REMOTE_JOB_STALE", got)
	}
	if got := deriveErrorCode(fmt.Errorf("remote final job is still rendering: %w", ErrFinalJobPending), StagePublishingDocuments); got != "REMOTE_RENDER_PENDING" {
		t.Fatalf("error code = %q, want the pending deferral kept apart", got)
	}
	if got := deriveErrorCode(errors.New("boom"), StagePublishingDocuments); got == "REMOTE_JOB_STALE" {
		t.Fatal("an unrelated failure must not borrow the stale-identity code")
	}
}

// TestCancelledRemotePreparationRecoversThroughTheFullLadder is the issue's
// recovery-path test on the real pipeline (every phase, driven exactly as the
// worker drives it): PREPARE, cancellation of the remote preparation, retry
// with a fresh remote id, and a successful finalize — while the completed
// script, audio and overlay work of the first attempt is REUSED, never
// regenerated.
func TestCancelledRemotePreparationRecoversThroughTheFullLadder(t *testing.T) {
	const runID = "run-stale-ladder"
	runner, repo, textGen, _, voiceoverGen, _ := newTestRunner()
	req := defaultTestRequest()
	req.GenerateTimeline = true
	req.FinalJob = true
	submitter := staleRecoveryStub()
	runner.SetFinalJobSubmitter(submitter)
	if err := repo.Create(context.Background(), &GenerationRun{
		ID: runID, JobID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Attempt 1: the whole ladder runs and dies on the canceled preparation.
	runner.Execute(context.Background(), runID, req)
	failed := awaitCompletion(t, repo, runID, 10*time.Second)
	if failed.Status != RunStatusFailed || failed.ErrorCode != "REMOTE_JOB_STALE" {
		t.Fatalf("attempt 1 = %+v, want FAILED/REMOTE_JOB_STALE", failed)
	}
	if failed.NextRetryAt == nil {
		t.Fatal("attempt 1 must schedule the fresh-identity retry")
	}
	textCalls, voiceCalls := textGen.callCount, voiceoverGen.callCount
	if textCalls == 0 {
		t.Fatal("precondition: attempt 1 must have generated the script")
	}

	// Attempt 2: the retry resumes at the final-job stage. The certified
	// artifacts of attempt 1 are reused (no regeneration) and the fresh remote
	// identity completes.
	runner.Execute(context.Background(), runID, req)
	completed := awaitCompletion(t, repo, runID, 10*time.Second)
	if completed.Status != RunStatusCompleted {
		t.Fatalf("attempt 2 status = %q (code=%q msg=%q), want COMPLETED", completed.Status, completed.ErrorCode, completed.ErrorMessage)
	}
	if textGen.callCount != textCalls || voiceoverGen.callCount != voiceCalls {
		t.Fatalf("attempt 2 regenerated work: text %d→%d, voiceover %d→%d — a final-job-only retry must reuse the certified artifacts",
			textCalls, textGen.callCount, voiceCalls, voiceoverGen.callCount)
	}
	if submitter.startCalls != 2 {
		t.Fatalf("StartFinalJob calls = %d, want 2 (prepare, then the fresh retry)", submitter.startCalls)
	}
	if want := []int{1, 2}; len(submitter.attempts) != 2 || submitter.attempts[0] != want[0] || submitter.attempts[1] != want[1] {
		t.Fatalf("attempt intents = %v, want [1 2]", submitter.attempts)
	}
	if completed.Result == nil || completed.Result.RemoteFinalJob == nil || completed.Result.RemoteFinalJob.Status != "SUCCEEDED" {
		t.Fatalf("final durable result = %+v, want the completed fresh render", completed.Result)
	}
	if len(completed.Result.RemoteFinalJobHistory) != 1 || completed.Result.RemoteFinalJobHistory[0].JobID != "master-cancelled" {
		t.Fatalf("attempt history = %+v, want the canceled identity archived", completed.Result.RemoteFinalJobHistory)
	}
	if len(submitter.attachCalls) != 1 || submitter.attachCalls[0].jobID != "master-fresh" {
		t.Fatalf("attach calls = %+v, want the finalize wait on the fresh identity", submitter.attachCalls)
	}
}

// TestStaleReceiptSettleKeepsTheMasterTerminalStatus: when the adapter already
// reported the Master's own terminal status, the archive preserves it instead
// of overwriting the recorded outcome.
func TestStaleReceiptSettleKeepsTheMasterTerminalStatus(t *testing.T) {
	const runID = "run-stale-keep-status"
	submitter := &stubFinalJobSubmitter{}
	result := &GenerateResult{}
	runner, repo := newFinalJobRunner(submitter, runID, nil)

	remote := RemoteFinalJobResult{JobID: "master-cancelled", Status: "CANCELED", Attempt: 1}
	if ok := runner.settleFinalJob(context.Background(), runID, result, remote, 0,
		fmt.Errorf("remote preparation master-cancelled is stale: %w", ErrFinalJobStaleReceipt)); ok {
		t.Fatal("a stale identity must not settle as success")
	}
	run, err := repo.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.Result == nil || run.Result.RemoteFinalJob == nil || run.Result.RemoteFinalJob.Status != "CANCELED" {
		t.Fatalf("durable receipt = %+v, want the Master's CANCELED status preserved", run.Result.RemoteFinalJob)
	}
}
