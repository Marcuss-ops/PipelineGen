package service

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
)

func TestWaitStateReturnsImmediatelyWhenTerminal(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(context.Background(), "job-1"); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	job, err := svc.WaitState(context.Background(), "job-1", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.State != model.StateCancelled {
		t.Fatalf("got %+v, want a cancelled terminal job", job)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("terminal job took %s; expected an immediate return", elapsed)
	}
}

func TestWaitStateWakesOnComplete(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	type result struct {
		job *model.Job
		err error
	}
	done := make(chan result, 1)
	go func() {
		job, err := svc.WaitState(context.Background(), "job-1", 10*time.Second)
		done <- result{job, err}
	}()

	// Park the waiter on the wake channel, then complete the job.
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	if err := svc.Complete(context.Background(), "job-1", "w1", model.Artifact{
		StorageKey: "abc", ArtifactHash: "abc", SizeBytes: 1, ContentType: "video/mp4",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.job == nil || got.job.State != model.StateCompleted {
			t.Fatalf("got %+v, want a completed job", got.job)
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Fatalf("completion wake took %s; expected event-driven wake", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitState did not wake on complete")
	}
}

func TestWaitStateTimesOutWithCurrentJob(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	job, err := svc.WaitState(context.Background(), "job-1", 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.State != model.StatePending {
		t.Fatalf("got %+v, want the current pending job", job)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("returned after %s; expected to honor the wait window", elapsed)
	}
}

func TestWaitStateUnknownJobFailsImmediately(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	start := time.Now()
	if _, err := svc.WaitState(context.Background(), "missing", 10*time.Second); err == nil {
		t.Fatal("unknown job must fail instead of waiting")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("unknown job took %s; expected an immediate error", elapsed)
	}
}

func TestWaitStateHonorsContextCancellation(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.WaitState(ctx, "job-1", 10*time.Second); err == nil {
		t.Fatal("cancelled context must surface as an error")
	}
}

func TestIsTerminalStateExcludesRendered(t *testing.T) {
	// `rendered` is durable but publication is still pending: a producer that
	// stopped waiting there could observe an artifact that is never published.
	for _, state := range []model.State{model.StatePending, model.StateRunning, model.StateRendered, model.StateFinalizing} {
		if IsTerminalState(state) {
			t.Fatalf("%s must not be terminal for a producer", state)
		}
	}
	for _, state := range []model.State{model.StateCompleted, model.StateFailed, model.StateCancelled} {
		if !IsTerminalState(state) {
			t.Fatalf("%s must be terminal", state)
		}
	}
}
