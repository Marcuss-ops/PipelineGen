package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
)

// validArtifact mirrors the queue-side completion gate used across tests.
func cancelTestArtifact() model.Artifact {
	return model.Artifact{StorageKey: "sha", ArtifactHash: "sha", SizeBytes: 1, ContentType: "video/mp4"}
}

func TestCancelPendingJobIsNeverClaimed(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-pending-cancel"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(context.Background(), "job-pending-cancel"); err != nil {
		t.Fatalf("cancel pending: %v", err)
	}
	job, err := svc.Get(context.Background(), "job-pending-cancel")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.StateCancelled {
		t.Fatalf("state = %q, want %q", job.State, model.StateCancelled)
	}
	if job.Attempts != 0 {
		t.Fatalf("attempts = %d, want 0 (Chronon was never invoked)", job.Attempts)
	}
	// A cancelled pending job must never be claimable, even across lease
	// expiry + repeated claim attempts.
	if claimed, _, err := svc.Claim(context.Background(), "worker-1"); err != nil || claimed != nil {
		t.Fatalf("claim after cancel = %v/%v, want nil job", claimed, err)
	}
	if n, err := svc.RequeueExpired(context.Background(), time.Now().Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("RequeueExpired after cancel = %d/%v, want 0", n, err)
	}
	if claimed, _, err := svc.Claim(context.Background(), "worker-2"); err != nil || claimed != nil {
		t.Fatalf("claim after lease cycle = %v/%v, want nil job", claimed, err)
	}
	job, _ = svc.Get(context.Background(), "job-pending-cancel")
	if job.Attempts != 0 {
		t.Fatalf("attempts after lease/claim cycle = %d, want 0", job.Attempts)
	}
}

func TestCancelRunningJobSurvivesLeaseClaimCycleWithoutNewChrononInvocation(t *testing.T) {
	svc := New(memory.New(10*time.Millisecond, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-running-cancel"}); err != nil {
		t.Fatal(err)
	}
	claimed, _, err := svc.Claim(context.Background(), "worker-1")
	if err != nil || claimed == nil {
		t.Fatalf("claim = %v/%v, want job", claimed, err)
	}
	// The worker invoked Chronon exactly once (attempt 1). The producer then
	// cancels the job while it is still leased/running.
	if err := svc.Cancel(context.Background(), "job-running-cancel"); err != nil {
		t.Fatalf("cancel running: %v", err)
	}
	job, _ := svc.Get(context.Background(), "job-running-cancel")
	if job.State != model.StateCancelled || job.Attempts != 1 {
		t.Fatalf("after cancel: state=%q attempts=%d, want cancelled/1", job.State, job.Attempts)
	}
	// The in-flight worker's terminal reports must be rejected: the job is no
	// longer running, so neither complete nor fail can move it or requeue it.
	if err := svc.Complete(context.Background(), "job-running-cancel", "worker-1", cancelTestArtifact()); err == nil {
		t.Fatal("complete on cancelled job must be rejected")
	}
	if err := svc.Fail(context.Background(), "job-running-cancel", "worker-1", "late failure"); err == nil {
		t.Fatal("fail on cancelled job must be rejected")
	}

	// The full lease/claim cycle: lease elapses, the expiry scan runs, and a
	// worker tries to claim again. The cancelled job must never come back, so
	// the Chronon invocation count (attempts) stays at exactly 1.
	for cycle := 0; cycle < 3; cycle++ {
		time.Sleep(15 * time.Millisecond) // let the lease elapse
		if n, err := svc.RequeueExpired(context.Background(), time.Now()); err != nil || n != 0 {
			t.Fatalf("cycle %d: RequeueExpired = %d/%v, want 0 affected (cancelled is terminal)", cycle, n, err)
		}
		if claimed, _, err := svc.Claim(context.Background(), "worker-2"); err != nil || claimed != nil {
			t.Fatalf("cycle %d: claim = %v/%v, want nil (cancelled is never claimable)", cycle, claimed, err)
		}
		job, _ := svc.Get(context.Background(), "job-running-cancel")
		if job.Attempts != 1 {
			t.Fatalf("cycle %d: attempts = %d, want 1 (no second Chronon invocation)", cycle, job.Attempts)
		}
		if job.State != model.StateCancelled {
			t.Fatalf("cycle %d: state = %q, want cancelled", cycle, job.State)
		}
	}
}

func TestCancelIsIdempotentAndRejectsTerminalStates(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	// Idempotent cancel.
	if err := svc.Submit(context.Background(), model.Job{ID: "job-double-cancel"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(context.Background(), "job-double-cancel"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(context.Background(), "job-double-cancel"); err != nil {
		t.Fatalf("second cancel must be a no-op: %v", err)
	}

	// Completed jobs are terminal: cancelling them is a conflict.
	if err := svc.Submit(context.Background(), model.Job{ID: "job-completed"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim(context.Background(), "worker-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Complete(context.Background(), "job-completed", "worker-1", cancelTestArtifact()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(context.Background(), "job-completed"); err == nil || !strings.Contains(err.Error(), "cannot cancel") {
		t.Fatalf("cancel completed = %v, want terminal-state error", err)
	}

	// Failed jobs are terminal too (maxAttempts=1 makes the first failure
	// permanent instead of requeuing).
	failedSvc := New(memory.New(30*time.Second, 1))
	if err := failedSvc.Submit(context.Background(), model.Job{ID: "job-failed"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := failedSvc.Claim(context.Background(), "worker-1"); err != nil {
		t.Fatal(err)
	}
	if err := failedSvc.Fail(context.Background(), "job-failed", "worker-1", "boom"); err != nil {
		t.Fatal(err)
	}
	if err := failedSvc.Cancel(context.Background(), "job-failed"); err == nil || !strings.Contains(err.Error(), "cannot cancel") {
		t.Fatalf("cancel failed = %v, want terminal-state error", err)
	}
}
