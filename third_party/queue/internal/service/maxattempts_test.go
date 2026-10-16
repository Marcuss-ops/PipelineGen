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

// TestServiceMaxAttemptsUnderConcurrentRetries submits many jobs and has
// several workers claim and fail them concurrently. Each job must be requeued
// exactly maxAttempts times in total: the final failure is permanent, so no
// job is ever attempted more than maxAttempts times even when workers race to
// re-claim it.
func TestServiceMaxAttemptsUnderConcurrentRetries(t *testing.T) {
	const maxAttempts = 3
	const jobs = 20
	const workers = 8

	svc := New(memory.New(30*time.Second, maxAttempts))
	for i := 0; i < jobs; i++ {
		if err := svc.Submit(context.Background(), model.Job{ID: fmt.Sprintf("job-%03d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	claimsPerJob := make(map[string]int)
	var wg sync.WaitGroup
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
				claimsPerJob[job.ID]++
				mu.Unlock()
				if err := svc.Fail(context.Background(), job.ID, worker, "boom"); err != nil {
					t.Errorf("fail(%s): %v", worker, err)
					return
				}
			}
		}(fmt.Sprintf("w%d", w))
	}
	wg.Wait()

	if len(claimsPerJob) != jobs {
		t.Fatalf("want %d distinct jobs claimed, got %d", jobs, len(claimsPerJob))
	}
	for id, claims := range claimsPerJob {
		if claims != maxAttempts {
			t.Errorf("job %s: want %d claims, got %d", id, maxAttempts, claims)
		}
		job, err := svc.Get(context.Background(), id)
		if err != nil {
			t.Errorf("get %s: %v", id, err)
			continue
		}
		if job.State != model.StateFailed {
			t.Errorf("job %s: want failed, got %s", id, job.State)
		}
		if job.Attempts != maxAttempts {
			t.Errorf("job %s: want attempts=%d, got %d", id, maxAttempts, job.Attempts)
		}
	}
	if s := svc.Stats(context.Background()); s.Failed != jobs || s.Pending != 0 || s.Running != 0 {
		t.Fatalf("want %d failed / 0 pending / 0 running, got %+v", jobs, s)
	}
}
