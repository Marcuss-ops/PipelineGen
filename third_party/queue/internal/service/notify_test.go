package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
)

func TestWaitAndClaimReturnsImmediatelyWhenJobAvailable(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	job, lease, err := svc.WaitAndClaim(context.Background(), "w1", "", 25*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil {
		t.Fatal("expected a job")
	}
	if lease <= 0 {
		t.Fatal("expected positive lease")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("claim took %s; expected immediate return", time.Since(start))
	}
}

func TestWaitAndClaimWakesOnSubmit(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	done := make(chan struct{})
	var (
		job *model.Job
		err error
	)
	go func() {
		defer close(done)
		job, _, err = svc.WaitAndClaim(context.Background(), "w1", "", 10*time.Second)
	}()
	// Give the waiter time to park on the wake channel, then submit.
	time.Sleep(150 * time.Millisecond)
	if err := svc.Submit(context.Background(), model.Job{ID: "wake-job"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("WaitAndClaim did not wake on submit")
	}
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.ID != "wake-job" {
		t.Fatalf("got job %+v, want wake-job", job)
	}
}

func TestWaitAndClaimTimesOutWithoutWork(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	start := time.Now()
	job, _, err := svc.WaitAndClaim(context.Background(), "w1", "", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if job != nil {
		t.Fatalf("expected no job, got %+v", job)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("returned after %s; expected to honor wait window", elapsed)
	}
}

func TestWaitAndClaimConcurrentSubmitWakeDeliversOnce(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		got []string
	)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, _, err := svc.WaitAndClaim(context.Background(), "w", "", 3*time.Second)
			if err != nil {
				t.Error(err)
				return
			}
			if job != nil {
				mu.Lock()
				got = append(got, job.ID)
				mu.Unlock()
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 2; i++ {
		if err := svc.Submit(context.Background(), model.Job{ID: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if len(got) != 2 {
		t.Fatalf("expected exactly 2 claims, got %v", got)
	}
}
