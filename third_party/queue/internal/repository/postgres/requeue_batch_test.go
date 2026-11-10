package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestRequeueExpiredDrainsInBatches pins the batch loop: with a batch size much
// smaller than the expired set, one sweep must still recover every job, across
// multiple committed transactions, instead of selecting the whole set in one
// operation that can exceed the repository timeout.
func TestRequeueExpiredDrainsInBatches(t *testing.T) {
	r := newRepo(t, time.Millisecond, 10)
	r.SetRequeueBatch(2)
	ctx := context.Background()

	const jobs = 7
	for i := 0; i < jobs; i++ {
		id := fmt.Sprintf("expired-%02d", i)
		if err := r.Submit(ctx, model.Job{ID: id, RenderPlan: []byte(`{}`)}); err != nil {
			t.Fatalf("submit %s: %v", id, err)
		}
		if _, _, err := r.Claim(ctx, fmt.Sprintf("w-%d", i)); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
	}
	// All leases are 1ms; give the last claim time to expire too.
	time.Sleep(20 * time.Millisecond)

	n, err := r.RequeueExpired(ctx, time.Now())
	if err != nil {
		t.Fatalf("RequeueExpired: %v (recovered %d)", err, n)
	}
	if n != jobs {
		t.Fatalf("RequeueExpired recovered %d jobs, want %d (a batch boundary must not strand the tail)", n, jobs)
	}
	for i := 0; i < jobs; i++ {
		id := fmt.Sprintf("expired-%02d", i)
		job, err := r.Get(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if job.State != model.StatePending {
			t.Fatalf("%s state = %q, want pending after lease expiry", id, job.State)
		}
		if job.Worker != "" || !job.LeaseUntil.IsZero() {
			t.Fatalf("%s still owned: worker=%q lease=%v", id, job.Worker, job.LeaseUntil)
		}
	}
}
