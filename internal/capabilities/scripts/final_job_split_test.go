package scriptgeneration

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type stubFinalJobSplit struct {
	stubFinalJobAttacher
	enabled          bool
	prepareCalls     int
	prepareReceipt   RemoteFinalJobResult
	prepareErr       error
	finalizeCalls    []string
	finalizeReceipt  RemoteFinalJobResult
	finalizeErr      error
	prepareStartedAt time.Time
}

func (s *stubFinalJobSplit) FinalJobSplitEnabled() bool { return s.enabled }

func (s *stubFinalJobSplit) PrepareFinalJobSource(_ context.Context, _ string, _ GenerateRequest, _ *GenerateResult) (RemoteFinalJobResult, error) {
	s.prepareCalls++
	s.prepareStartedAt = time.Now()
	time.Sleep(2 * time.Millisecond)
	return s.prepareReceipt, s.prepareErr
}

func (s *stubFinalJobSplit) FinalizeFinalJobSource(_ context.Context, jobID, _ string, _ GenerateRequest, _ *GenerateResult) (RemoteFinalJobResult, error) {
	s.finalizeCalls = append(s.finalizeCalls, jobID)
	return s.finalizeReceipt, s.finalizeErr
}

func TestPreparedSplitReceiptIsFinalizedThenAttachedWithoutASecondPrepare(t *testing.T) {
	const runID = "run-split-finalize"
	submitter := &stubFinalJobSplit{
		stubFinalJobAttacher: stubFinalJobAttacher{
			attachResult: RemoteFinalJobResult{JobID: "master-split", Status: "SUCCEEDED"},
		},
		enabled: true,
		finalizeReceipt: RemoteFinalJobResult{
			JobID: "master-split", Status: "RUNNING",
			Timing: &RemoteFinalJobTiming{FinalizeMS: 7},
		},
	}
	result := &GenerateResult{RemoteFinalJob: &RemoteFinalJobResult{
		JobID: "master-split", Status: "PREPARED", Attempt: 1, Split: true,
		Timing: &RemoteFinalJobTiming{HandoffMS: 11, PayloadMS: 9, PrepareMS: 1},
	}}
	runner, repo := newFinalJobRunner(submitter, runID, result)
	if ok := runner.submitFinalJob(context.Background(), runID, defaultTestRequest(), result); !ok {
		t.Fatal("a PREPARED split job must finalize, attach, and complete")
	}
	if submitter.submits != 0 {
		t.Fatalf("SubmitFinalJob calls = %d, want 0 — a PREPARED job must never be submitted again", submitter.submits)
	}
	if len(submitter.finalizeCalls) != 1 || submitter.finalizeCalls[0] != "master-split" {
		t.Fatalf("finalize calls = %v, want exactly master-split", submitter.finalizeCalls)
	}
	if len(submitter.attachCalls) != 1 || submitter.attachCalls[0].jobID != "master-split" {
		t.Fatalf("attach calls = %+v, want the finalized master-split job", submitter.attachCalls)
	}
	run, err := repo.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	receipt := run.Result.RemoteFinalJob
	if receipt == nil || receipt.Status != "SUCCEEDED" {
		t.Fatalf("durable receipt = %+v, want the terminal attach result", receipt)
	}
	if receipt.Timing == nil || receipt.Timing.HandoffMS != 11 || receipt.Timing.FinalizeMS != 7 {
		t.Fatalf("timing = %+v, want the base handoff and the late FINALIZE both preserved", receipt.Timing)
	}
}

func splitTestRequest() GenerateRequest {
	req := defaultTestRequest()
	req.FinalJob = true
	return req
}

func TestSplitPrepareReplaysADurablePrepareIntent(t *testing.T) {
	const runID = "run-split-replay"
	submitter := &stubFinalJobSplit{
		enabled:        true,
		prepareReceipt: RemoteFinalJobResult{JobID: "master-replay", Status: "PREPARED", Attempt: 1, Split: true},
	}
	result := &GenerateResult{
		RemoteFinalJob: &RemoteFinalJobResult{Status: "PREPARE_INTENT", Attempt: 1, Split: true},
		AudioOutput:    AudioOutput{FinalAudio: certifiedFinalAudio(1000)},
	}
	runner, _ := newFinalJobRunner(submitter, runID, result)
	done := runner.startFinalJobSplitPrepare(context.Background(), runID, splitTestRequest(), result)
	if done == nil {
		t.Fatal("a durable split PREPARE_INTENT must be replayed, not silently dropped")
	}
	outcome := <-done
	overlayFinished := outcome.finished.Add(time.Second)
	runner.applyFinalJobSplitPrepare(context.Background(), runID, result, outcome, overlayFinished)
	if submitter.prepareCalls != 1 {
		t.Fatalf("PrepareFinalJobSource calls = %d, want 1", submitter.prepareCalls)
	}
	if result.RemoteFinalJob == nil || result.RemoteFinalJob.Status != "PREPARED" || result.RemoteFinalJob.JobID != "master-replay" {
		t.Fatalf("result receipt = %+v, want the replayed PREPARED job", result.RemoteFinalJob)
	}
	if result.RemoteFinalJob.Timing == nil || result.RemoteFinalJob.Timing.SplitPrepareMS <= 0 || result.RemoteFinalJob.Timing.SplitOverlapMS <= 0 {
		t.Fatalf("timing = %+v, want the measured split prepare/overlap", result.RemoteFinalJob.Timing)
	}
}

func TestSplitPrepareDoesNotRunForATerminalOrPreparedReceipt(t *testing.T) {
	for _, receipt := range []*RemoteFinalJobResult{
		{JobID: "master-prepared", Status: "PREPARED", Attempt: 1, Split: true},
		{JobID: "master-terminal", Status: "FAILED", Attempt: 1, Split: true},
		{JobID: "master-running", Status: "RUNNING", Attempt: 1, Split: true},
	} {
		submitter := &stubFinalJobSplit{enabled: true}
		result := &GenerateResult{RemoteFinalJob: receipt, AudioOutput: AudioOutput{FinalAudio: certifiedFinalAudio(1000)}}
		runner, _ := newFinalJobRunner(submitter, "run-split-skip", result)
		if done := runner.startFinalJobSplitPrepare(context.Background(), "run-split-skip", splitTestRequest(), result); done != nil {
			t.Fatalf("receipt %+v started a second PREPARE", receipt)
		}
		if submitter.prepareCalls != 0 {
			t.Fatalf("receipt %+v called PrepareFinalJobSource %d times", receipt, submitter.prepareCalls)
		}
	}
}

func TestSplitPrepareNotSentClearsTheIntentForTheSerialHandoff(t *testing.T) {
	const runID = "run-split-not-sent"
	submitter := &stubFinalJobSplit{enabled: true, prepareErr: fmt.Errorf("%w: capability unavailable", ErrFinalJobSplitPrepareNotSent)}
	result := &GenerateResult{AudioOutput: AudioOutput{FinalAudio: certifiedFinalAudio(1000)}}
	runner, _ := newFinalJobRunner(submitter, runID, result)
	done := runner.startFinalJobSplitPrepare(context.Background(), runID, splitTestRequest(), result)
	if done == nil {
		t.Fatal("want an early PREPARE attempt")
	}
	outcome := <-done
	runner.applyFinalJobSplitPrepare(context.Background(), runID, result, outcome, outcome.finished)
	if result.RemoteFinalJob != nil {
		t.Fatalf("receipt = %+v, want the unsent split intent cleared for the serial handoff", result.RemoteFinalJob)
	}
}

func TestSubmitReplaysAnAmbiguousSplitIntentBeforeSerial(t *testing.T) {
	const runID = "run-split-replay-submit"
	submitter := &stubFinalJobSplit{
		stubFinalJobAttacher: stubFinalJobAttacher{
			attachResult: RemoteFinalJobResult{JobID: "master-replayed", Status: "SUCCEEDED"},
		},
		enabled:        true,
		prepareReceipt: RemoteFinalJobResult{JobID: "master-replayed", Status: "PREPARED", Attempt: 1, Split: true},
		finalizeReceipt: RemoteFinalJobResult{
			JobID: "master-replayed", Status: "RUNNING", Split: true,
		},
	}
	result := &GenerateResult{
		AudioOutput:    AudioOutput{FinalAudio: certifiedFinalAudio(1000)},
		RemoteFinalJob: &RemoteFinalJobResult{Status: "PREPARE_INTENT", Attempt: 1, Split: true},
	}
	runner, _ := newFinalJobRunner(submitter, runID, result)
	if ok := runner.submitFinalJob(context.Background(), runID, splitTestRequest(), result); !ok {
		t.Fatal("an ambiguous split intent must replay the base PREPARE, finalize it, and attach")
	}
	if submitter.prepareCalls != 1 {
		t.Fatalf("prepare calls = %d, want the one same-key replay", submitter.prepareCalls)
	}
	if len(submitter.finalizeCalls) != 1 || submitter.finalizeCalls[0] != "master-replayed" {
		t.Fatalf("finalize calls = %v, want the replayed job finalized", submitter.finalizeCalls)
	}
	if submitter.submits != 0 {
		t.Fatalf("serial submits = %d, want 0 after a successful replay", submitter.submits)
	}
}

func TestSubmitReusesACompletedReplayedJobWithoutSubmittingAgain(t *testing.T) {
	const runID = "run-split-replay-complete"
	submitter := &stubFinalJobSplit{
		enabled:        true,
		prepareReceipt: RemoteFinalJobResult{JobID: "master-done", Status: "SUCCEEDED", Attempt: 1, Split: true},
	}
	result := &GenerateResult{
		AudioOutput:    AudioOutput{FinalAudio: certifiedFinalAudio(1000)},
		RemoteFinalJob: &RemoteFinalJobResult{Status: "PREPARE_INTENT", Attempt: 1, Split: true},
	}
	runner, _ := newFinalJobRunner(submitter, runID, result)
	if ok := runner.submitFinalJob(context.Background(), runID, splitTestRequest(), result); !ok {
		t.Fatal("a replay that reconciles to a completed remote job must succeed")
	}
	if submitter.submits != 0 {
		t.Fatalf("serial submits = %d, want 0 for an already completed replay", submitter.submits)
	}
	if result.RemoteFinalJob == nil || result.RemoteFinalJob.Status != "SUCCEEDED" || result.RemoteFinalJob.JobID != "master-done" {
		t.Fatalf("receipt = %+v, want the completed replayed job", result.RemoteFinalJob)
	}
}

func TestSubmitUsesSerialWhenTheSplitReplayWasNotSent(t *testing.T) {
	const runID = "run-split-replay-not-sent"
	submitter := &stubFinalJobSplit{
		enabled:     true,
		prepareErr:  fmt.Errorf("%w: no capability", ErrFinalJobSplitPrepareNotSent),
		finalizeErr: nil,
	}
	submitter.stubFinalJobAttacher.stubFinalJobSubmitter.result = RemoteFinalJobResult{JobID: "master-serial", Status: "SUCCEEDED"}
	result := &GenerateResult{
		AudioOutput:    AudioOutput{FinalAudio: certifiedFinalAudio(1000)},
		RemoteFinalJob: &RemoteFinalJobResult{Status: "PREPARE_INTENT", Attempt: 1, Split: true},
	}
	runner, _ := newFinalJobRunner(submitter, runID, result)
	if ok := runner.submitFinalJob(context.Background(), runID, splitTestRequest(), result); !ok {
		t.Fatal("a not-sent split replay must fall back to the serial handoff")
	}
	if submitter.submits != 1 {
		t.Fatalf("serial submits = %d, want 1", submitter.submits)
	}
	if result.RemoteFinalJob == nil || result.RemoteFinalJob.JobID != "master-serial" || result.RemoteFinalJob.Status != "SUCCEEDED" {
		t.Fatalf("receipt = %+v, want the serial success receipt", result.RemoteFinalJob)
	}
}

func TestSplitPrepareFailureLeavesTheSerialHandoffAvailable(t *testing.T) {
	const runID = "run-split-fallback"
	submitter := &stubFinalJobSplit{enabled: true, prepareErr: context.DeadlineExceeded}
	result := &GenerateResult{AudioOutput: AudioOutput{FinalAudio: certifiedFinalAudio(1000)}}
	runner, _ := newFinalJobRunner(submitter, runID, result)
	done := runner.startFinalJobSplitPrepare(context.Background(), runID, splitTestRequest(), result)
	if done == nil {
		t.Fatal("want an early PREPARE attempt")
	}
	outcome := <-done
	runner.applyFinalJobSplitPrepare(context.Background(), runID, result, outcome, outcome.finished)
	if result.RemoteFinalJob == nil || result.RemoteFinalJob.Status != "PREPARE_INTENT" || !result.RemoteFinalJob.Split {
		t.Fatalf("receipt = %+v, want the durable split intent for the serial fallback", result.RemoteFinalJob)
	}
	if result.RemoteFinalJob.JobID != "" {
		t.Fatalf("receipt = %+v, want no invented job id", result.RemoteFinalJob)
	}
}
