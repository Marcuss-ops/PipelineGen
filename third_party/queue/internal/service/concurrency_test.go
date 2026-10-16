package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
)

// TestServiceConcurrentClaimExclusive runs many workers claiming against the
// same queue concurrently and asserts no job is ever handed to two workers.
// The in-memory backend is the default, so this exercises the service + the
// repository contract together (and is meaningful under -race).
func TestServiceConcurrentClaimExclusive(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))

	const jobs = 100
	for i := 0; i < jobs; i++ {
		if err := svc.Submit(context.Background(), model.Job{ID: fmt.Sprintf("job-%03d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	claimed := make(map[string]string) // jobID -> workerID
	var wg sync.WaitGroup
	for w := 0; w < 20; w++ {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			for {
				job, lease, err := svc.Claim(context.Background(), worker)
				if err != nil {
					t.Errorf("claim(%s): %v", worker, err)
					return
				}
				if job == nil {
					return
				}
				if lease <= 0 {
					t.Errorf("claim(%s): non-positive lease %s", worker, lease)
					return
				}
				mu.Lock()
				if prev, dup := claimed[job.ID]; dup {
					t.Errorf("job %s claimed by both %s and %s", job.ID, prev, worker)
				}
				claimed[job.ID] = worker
				mu.Unlock()
			}
		}(fmt.Sprintf("w%d", w))
	}
	wg.Wait()

	if len(claimed) != jobs {
		t.Fatalf("want %d distinct claims, got %d", jobs, len(claimed))
	}
	if s := svc.Stats(context.Background()); s.Running != jobs || s.Pending != 0 {
		t.Fatalf("want %d running / 0 pending, got %+v", jobs, s)
	}
}

// TestServiceConcurrentRenewSingleOwner hammers Renew from many goroutines on
// the same owned job and asserts every renew succeeds and the lease stays
// fresh (the job must not be requeued).
func TestServiceConcurrentRenewSingleOwner(t *testing.T) {
	svc := New(memory.New(time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	const goroutines = 20
	const renews = 100
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*renews)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < renews; i++ {
				if err := svc.Renew(context.Background(), "job-1", "w1"); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("renew: %v", err)
	}

	if s := svc.Stats(context.Background()); s.Running != 1 {
		t.Fatalf("job should still be running, got %+v", s)
	}
	if n, err := svc.RequeueExpired(context.Background(), time.Now()); err != nil || n != 0 {
		t.Fatalf("job should hold a fresh lease: requeued=%d err=%v", n, err)
	}
}

// TestServiceConcurrentRequeueThenClaim lets every lease expire, requeues the
// expired jobs and then re-claims them from many workers concurrently. Each
// job must be claimed exactly once by the second generation, with its attempt
// count incremented (history preserved, not overwritten).
func TestServiceConcurrentRequeueThenClaim(t *testing.T) {
	const jobs = 50
	const lease = 20 * time.Millisecond
	svc := New(memory.New(lease, 3))

	for i := 0; i < jobs; i++ {
		if err := svc.Submit(context.Background(), model.Job{ID: fmt.Sprintf("job-%03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	// One worker claims everything so all jobs are running and leased.
	for i := 0; i < jobs; i++ {
		job, _, err := svc.Claim(context.Background(), "w0")
		if err != nil || job == nil {
			t.Fatalf("initial claim %d: job=%v err=%v", i, job, err)
		}
	}

	time.Sleep(2 * lease) // let all leases expire

	// Requeue the expired leases, then re-claim concurrently.
	if n, err := svc.RequeueExpired(context.Background(), time.Now()); err != nil || n != jobs {
		t.Fatalf("requeue expired: want %d, got %d err=%v", jobs, n, err)
	}

	var mu sync.Mutex
	claimed := make(map[string]int) // jobID -> attempts seen by the re-claimer
	var wg sync.WaitGroup
	const workers = 10
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			for {
				job, _, err := svc.Claim(context.Background(), worker)
				if err != nil {
					t.Errorf("claim(%s): %v", worker, err)
					return
				}
				if job == nil {
					return
				}
				mu.Lock()
				if prev, dup := claimed[job.ID]; dup {
					t.Errorf("job %s claimed twice after requeue (attempts %d and %d)", job.ID, prev, job.Attempts)
				}
				claimed[job.ID] = job.Attempts
				mu.Unlock()
			}
		}(fmt.Sprintf("w%d", w))
	}
	wg.Wait()

	if len(claimed) != jobs {
		t.Fatalf("want %d second-generation claims, got %d", jobs, len(claimed))
	}
	for id, attempts := range claimed {
		if attempts != 2 {
			t.Errorf("job %s: want attempts=2, got %d", id, attempts)
		}
	}
	if s := svc.Stats(context.Background()); s.Running != jobs || s.Pending != 0 {
		t.Fatalf("all jobs should be running again, got %+v", s)
	}
}
